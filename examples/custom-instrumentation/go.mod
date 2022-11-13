module httpbin-instrumentation

go 1.18

require (
	github.com/DataDog/datadog-go v4.8.3+incompatible
	github.com/mccutchen/go-httpbin/v2 v2.5.1
)

require (
	github.com/Microsoft/go-winio v0.6.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/stretchr/objx v0.3.0 // indirect
	github.com/stretchr/testify v1.3.0 // indirect
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/sys v0.2.0 // indirect
	golang.org/x/tools v0.3.0 // indirect
)

// Always build against the local version, to make it easier to update examples
// in sync with changes to go-httpbin
replace github.com/mccutchen/go-httpbin/v2 => ../..
