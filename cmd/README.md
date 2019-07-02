# What is going on here?

## TL;DR

 * `cmd/maincmd` package exposes all of this app's command line functionality in a `Main()`

 * `cmd/go-httpbin` and `cmd/go_httpbin` build identical binaries using the
   `maincmd` package for backwards compatibility reasons explained below

## Why tho

Originally, this project exposed only one command:

    cmd/go-httpbin/main.go

But the dash in that path was incompatible with Google App Engine's naming
restrictions, so in [moving httpbingo.org onto Google App Engine][pr17], that
path was (carelessly) renamed to

    cmd/go_httpbin/main.go

_That_ change had a number of unintended consequences:

 * It broke existing workflows built around `go get github.com/mccutchen/go-httpbin/cmd/go-httpbin`,
   as suggested in the README

 * It broke the Makefile, which was still looking for `cmd/go-httpbin`

 * It broke the absolute aesthetic truth that CLI binaries should use dashes
   instead of underscores for word separators

So, to restore the former behavior while maintaining support for deploying to
App Engine, the actual main functionality was extracted into the `cmd/maincmd`
package here and shared between the other two.

(This is pretty dumb, I know, but it seems to work.)

[pr17]: https://github.com/mccutchen/go-httpbin/pull/17
