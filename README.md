# rotate
`rotate` is a file rolling package for Go

## How to use
```go
writer, _ := rotate.NewRotateWriter(
    "/var/log/myapp/foo.log",
    rotate.WithMaxSize(500),   // megabytes
    rotate.WithKeepDays(30),   // days
    rotate.WithMaxBackups(100), 
)
log.SetOutput(&writer)
```