package main

import (
	"context"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"sync"

	"flag"

	"path"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

var baseDir = flag.String("baseDir", "", "Directory to copy s3 contents to. (required)")
var bucket = flag.String("bucket", "", "S3 Bucket to copy contents from. (required)")
var concurrency = flag.Int("concurrency", 10, "Number of concurrent connections to use.")
var queueSize = flag.Int("queueSize", 100, "Size of the queue")

func main() {
	flag.Parse()
	if len(*baseDir) == 0 || len(*bucket) == 0 {
		flag.Usage()
		os.Exit(-1)
	}

	sess, err := session.NewSession()
	if err != nil {
		log.Fatalf("Failed to create a new session. %v", err)
	}
	s3Client := s3.New(sess)

	DownloadBucket(s3Client, *bucket, *baseDir, *concurrency, *queueSize)
}

func DownloadBucket(client *s3.S3, bucket, baseDir string, concurrency, queueSize int) {
	keysChan := make(chan string, queueSize)
	cpyr := &Copier{
		client:  client,
		bucket:  bucket,
		baseDir: baseDir,
		bufPool: &sync.Pool{
			New: func() interface{} {
				return make([]byte, 1024*16)
			},
		}}
	wg := new(sync.WaitGroup)
	statsTracker := NewStatsTracker(1 * time.Second)
	defer statsTracker.Stop()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keysChan {
				n, err := cpyr.Copy(key)
				if err != nil {
					log.Printf("Failed to download key %v, due to %v", key, err)
				}
				statsTracker.Inc(n)
			}
		}()
	}

	dc := &DirectoryCreator{baseDir: baseDir, dirsSeen: make(map[string]bool), newDirPermission: 0755}
	req := &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}
	err := client.ListObjectsV2Pages(req, func(resp *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, content := range resp.Contents {
			key := *content.Key
			if err := dc.MkDirIfNeeded(key); err != nil {
				log.Fatalf("Failed to create directory for key %v due to %v", key, err)
			}
			keysChan <- key
		}
		return true
	})
	close(keysChan)
	if err != nil {
		log.Printf("Failed to list objects for bucket %v: %v", bucket, err)
	}
	wg.Wait()
}

type DirectoryCreator struct {
	dirsSeen         map[string]bool
	baseDir          string
	newDirPermission os.FileMode
}

func (dc *DirectoryCreator) MkDirIfNeeded(key string) error {
	if lastIdx := strings.LastIndex(key, "/"); lastIdx != -1 {
		prefix := key[:lastIdx]
		if _, ok := dc.dirsSeen[prefix]; !ok {
			dirPath := path.Join(dc.baseDir, prefix)
			if err := os.MkdirAll(dirPath, dc.newDirPermission); err != nil {
				return err
			}
			dc.dirsSeen[prefix] = true
		}
	}
	return nil
}

type Copier struct {
	client  *s3.S3
	bucket  string
	baseDir string
	bufPool *sync.Pool
}

func (c *Copier) Copy(key string) (int64, error) {
	op, err := c.client.GetObjectWithContext(context.Background(), &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}, func(r *request.Request) {
		r.HTTPRequest.Header.Add("Accept-Encoding", "gzip")
	})
	if err != nil {
		return 0, err
	}
	defer op.Body.Close()

	f, err := os.Create(path.Join(c.baseDir, key))
	if err != nil {
		io.Copy(ioutil.Discard, op.Body)
		return 0, err
	}
	defer f.Close()

	buf := c.bufPool.Get().([]byte)
	n, err := io.CopyBuffer(f, op.Body, buf)
	c.bufPool.Put(buf)
	return n, err
}

type StatsTracker struct {
	startTime time.Time
	ticker    *time.Ticker

	count             uint64
	totalBytesWritten int64
}

func NewStatsTracker(logStatDuration time.Duration) *StatsTracker {
	s := &StatsTracker{startTime: time.Now(), ticker: time.NewTicker(logStatDuration), totalBytesWritten: 0}
	go s.start()
	return s
}

func (s *StatsTracker) Inc(writtenBytes int64) {
	atomic.AddInt64(&s.totalBytesWritten, writtenBytes)
	atomic.AddUint64(&s.count, 1)
}

func (s *StatsTracker) Stop() {
	duration := time.Now().Sub(s.startTime)
	log.Printf("Total number of files: %d, Total time taken: %v, Transfer rate %.4f MiB/s", s.count, duration, throughputInMiB(s.totalBytesWritten, duration))
	s.ticker.Stop()
}

func (s *StatsTracker) start() {
	previouslyPrintedTime := s.startTime
	var previouslyWrittenBytes int64
	for currentTime := range s.ticker.C {
		currentCount := atomic.LoadUint64(&s.count)
		if currentCount == 0 {
			continue
		}
		totalBytesWritten := atomic.LoadInt64(&s.totalBytesWritten)
		log.Printf("Copied %v files from s3 in %v (%.4f MiB/s)\n",
			currentCount,
			currentTime.Sub(s.startTime),
			throughputInMiB(totalBytesWritten-previouslyWrittenBytes, currentTime.Sub(previouslyPrintedTime)))
		previouslyPrintedTime = currentTime
		previouslyWrittenBytes = totalBytesWritten
	}
}

func throughputInMiB(bytesWritten int64, duration time.Duration) float64 {
	return float64(bytesWritten) / duration.Seconds() / (1024 * 1024)
}
