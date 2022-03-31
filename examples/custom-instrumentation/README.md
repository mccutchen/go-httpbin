# Custom Instrumentation

This example demonstrates how to use go-httpbin's [`Observer`][1] mechanism to
add custom instrumentation to a go-httpbin instance.

An _observer_ is a function that will be called with an [`httpbin.Result`][2]
struct after every request, which provides a hook for custom logging, metrics,
or other instrumentation.

Note: This does require building your own small wrapper around go-httpbin, as
you can see in [main.go](./main.go) here.  That's because go-httpbin has no
dependencies outside of the Go stdlib, to make sure that it is as
safe/lightweight as possible to include as a dependency in other applications'
test suites where useful.

[1]: https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2/httpbin#Observer
[2]: https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2/httpbin#Result
