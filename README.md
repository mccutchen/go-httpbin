# go-httpbin

A WIP golang port of https://httpbin.org/.

## Testing

```
go test
go test -cover
go test -coverprofile=cover.out && go tool cover -html=cover.out
```

## Running

```
go build && ./go-httpbin
```
