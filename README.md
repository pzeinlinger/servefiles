# servefiles

[![GoDoc](https://img.shields.io/badge/api-Godoc-blue.svg)](https://pkg.go.dev/github.com/rickb777/servefiles)
[![Build Status](https://travis-ci.org/rickb777/servefiles.svg?branch=master)](https://travis-ci.org/rickb777/servefiles/builds)
[![Coverage Status](https://coveralls.io/repos/rickb777/servefiles/badge.svg?branch=master&service=github)](https://coveralls.io/github/rickb777/servefiles?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/rickb777/servefiles)](https://goreportcard.com/report/github.com/rickb777/servefiles)
[![Issues](https://img.shields.io/github/issues/rickb777/servefiles.svg)](https://github.com/rickb777/servefiles/issues)

Serve static files from a Go http server, including performance-enhancing features.

- Based on the standard net/http ServeFiles, with gzip/brotli and cache performance enhancements.

Please see the [GoDoc](https://godoc.org/github.com/rickb777/servefiles) for more.

## Installation

    go get -u github.com/daubit/servefiles/v3

## MaxAge

User agents can cache responses. This http server enables easy support for two such mechanisms:

- Conditional requests (using `etags`) allow the response to be sent only when it has changed
- MaxAge response headers allow the user agent to cache entities until some expiry time.

Note that conditional requests (RFC7232) and MaxAge caching (RFC7234) can work together as required. Conditional requests still require network round trips, whereas caching removes all network round-trips until the entities reach their expiry time.

## SPA support

There is support for serving SPA webpage by using WithSPA() this serves index.html for all resources that do not have a file extension

## Gin Adapter

Sub-package gin_adapter provides integration hooks into the [Gin web framework](github.com/gin-gonic/gin). This makes it easy for Gin code to use this asset handler also: see the example in the sub-package for more info.

## v3

Version 3 brings Go module support. Also, `brotli` encoding is supported alongside `gzip` encoding. Brotli now has widespread implementation in most browsers. You can compress your textual assets (including Javascript, CSS, HTML, SVG etc) using Brotli and/or Gzip as part of your build pipeline, uploading both the original and compressed files to your production server's asset directories. Brotli compression takes longer than Gzip but produces more compact files. Compression is, of course, optional.

## Earlier versions

Earlier versions do not support Go modules, nor `brotli` encoding, although `gzip` encoding is supported.

## Status

This library has been in reliable production use for some time. Versioning follows the well-known semantic version pattern.

## Licence

[MIT](LICENSE)
