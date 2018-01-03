# AWS S3 Go Copier

A simple Go CLI to copy an S3 bucket to a filesystem. The tool is tuned to copy large number of small images.

For more detail please read https://medium.com/@venks.sa/copying-data-from-s3-to-ebs-30x-faster-using-go-e2cdb1093284


## How to build this

It was tested using go 1.8 and 1.9. [Install link](https://golang.org/doc/install#osx). Needs dep as well.

```
# To fetch aws go sdk.
go get -u github.com/venkssa/s3copier

cd ~/go/src/github.com/venkssa/s3copier
dep ensure
```

### How to build a binary to run locally
```
go build -o s3copier copier.go
```

This creates a devtoolsslackbot binary which you can run by
```
export AWS_PROFILE=<profile> # set if you need to use a non-default profile.
export AWS_REGION=<region> 
./s3copier -bucket=<bucketname> -baseDir=<path> -concurrency=<number of concurrent connections to use> -queueSize=<number of keys to queue up to copy>
```

To build a LINUX binary on Mac
```
GOOS=linux GOARCH=amd64 go build -o s3copier copier.go
```
