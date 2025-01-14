// MIT License
//
// Copyright (c) 2016 Rick Beton
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package servefiles

import (
	"fmt"
	"io/fs"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rickb777/path"
	"github.com/spf13/afero"
)

// This needs to track the same string in net/http (which is unlikely ever to change)
const indexPage = "index.html"

type CacheDirective = string

const (
	CacheDirectivePublic    CacheDirective = "public"
	CacheDirectivePrivate   CacheDirective = "private"
	CacheDirectiveImmutable CacheDirective = "immutable"
)

// Assets sets the options for asset handling. Use AssetHandler to create the handler(s) you need.
type Assets struct {
	// Choose a number greater than zero to strip off some leading segments from the URL path. This helps if
	// you want, say, a sequence number in the URL so that only has the effect of managing far-future cache
	// control. Use zero for default behaviour.
	UnwantedPrefixSegments int

	// Set the expiry duration for assets. This will be set via headers in the response. This should never be
	// negative. Use zero to disable asset caching in clients and proxies.
	MaxAge time.Duration

	// Configurable http.Handler which is called when no matching route is found. If it is not set,
	// http.NotFound is used.
	NotFound http.Handler

	// the local filesystem (remember that all paths are relative to its root)
	fs               fs.FS
	server           http.Handler
	expiryElasticity time.Duration
	timestamp        int64
	timestampExpiry  string
	lock             *sync.Mutex
	Spa              bool
	CacheDirectives  []CacheDirective
}

// Type conformance proof
var _ http.Handler = &Assets{}

//-------------------------------------------------------------------------------------------------

// NewAssetHandler creates an Assets value. The parameter is the directory containing the asset files;
// this can be absolute or relative to the directory in which the server process is started.
//
// This function cleans (i.e. normalises) the asset path.
func NewAssetHandler(assetPath string) *Assets {
	cleanAssetPath := path.Clean(assetPath)
	Debugf("NewAssetHandler %s\n", cleanAssetPath)
	filesystem := os.DirFS(cleanAssetPath).(fs.StatFS)
	return NewAssetHandlerIoFS(filesystem)
}

// NewAssetHandlerFS creates an Assets value for a given filesystem.
func NewAssetHandlerFS(fs afero.Fs) *Assets {
	return &Assets{
		fs:     afero.NewIOFS(fs),
		server: http.FileServer(afero.NewHttpFs(fs)),
		lock:   &sync.Mutex{},
	}
}

// NewAssetHandlerIoFS creates an Assets value for a given filesystem.
// Implementations include os.DirFS.
func NewAssetHandlerIoFS(fs fs.FS) *Assets {
	return &Assets{
		fs:     fs,
		server: http.FileServer(http.FS(fs)),
		lock:   &sync.Mutex{},
	}
}

// StripOff alters the handler to strip off a specified number of segments from the path before
// looking for the matching asset. For example, if StripOff(2) has been applied, the requested
// path "/a/b/c/d/doc.js" would be shortened to "c/d/doc.js".
//
// The returned handler is a new copy of the original one.
func (a Assets) StripOff(unwantedPrefixSegments int) *Assets {
	if unwantedPrefixSegments < 0 {
		panic("Negative unwantedPrefixSegments")
	}
	a.UnwantedPrefixSegments = unwantedPrefixSegments
	return &a
}

// WithMaxAge alters the handler to set the specified max age on the served assets.
// Defaults to cache directive public
// The returned handler is a new copy of the original one.
func (a Assets) WithMaxAge(maxAge time.Duration) *Assets {
	if maxAge < 0 {
		panic("Negative maxAge")
	}
	a.MaxAge = maxAge
	a.CacheDirectives = []CacheDirective{CacheDirectivePublic}
	return &a
}

// WithNotFound alters the handler so that 404-not found cases are passed to a specified
// handler. Without this, the default handler is the one provided in the net/http package.
//
// The returned handler is a new copy of the original one.
func (a Assets) WithNotFound(notFound http.Handler) *Assets {
	a.NotFound = notFound
	return &a
}

// WithSPA alters the handler so that all requestet files without a file extention instead return index.html
//
// The returned handler is a new copy of the original one.
func (a Assets) WithSPA() *Assets {
	a.Spa = true
	return &a
}

func (a Assets) WithCacheDirective(ds ...CacheDirective) *Assets {
	a.CacheDirectives = ds
	return &a
}

//-------------------------------------------------------------------------------------------------

func isSPARequest(resource string) bool {
	// two cases
	// 1. there is no dot -> "/" or some other path was requested
	// 2. there is a dot, so check if the last dot is after the last slash, if that is a case it is a filepath
	if strings.Count(resource, ".") == 0 {
		return true
	}
	lastDot := strings.LastIndex(resource, ".")
	lastSlash := strings.LastIndex(resource, "/")
	if lastDot < lastSlash {
		return true
	}
	return false
}

type fileData struct {
	resource string
	code     code
	fi       os.FileInfo
}

func calculateEtag(fi os.FileInfo) string {
	if fi == nil {
		return ""
	}
	return fmt.Sprintf(`"%x-%x"`, fi.ModTime().Unix(), fi.Size())
}

func handleSaturatedServer(header http.Header, resource string, err error) fileData {
	// Possibly the server is under heavy load and ran out of file descriptors
	backoff := 2 + rand.Int31()%4 // 2–6 seconds to prevent a stampede
	header.Set("Retry-After", strconv.Itoa(int(backoff)))
	return fileData{resource, ServiceUnavailable, nil}
}

func (a *Assets) checkResource(resource string, header http.Header) fileData {
	d, err := fs.Stat(a.fs, removeLeadingSlash(resource))
	if err != nil {
		if os.IsNotExist(err) {
			// gzipped does not exist; original might but this gets checked later
			Debugf("Assets checkResource 404 %s\n", resource)
			return fileData{"", NotFound, nil}

		} else if os.IsPermission(err) {
			// incorrectly assembled gzipped asset is treated as an error
			Debugf("Assets checkResource 403 %s\n", resource)
			return fileData{resource, Forbidden, nil}
		}

		Debugf("Assets handleSaturatedServer 503 %s\n", resource)
		return handleSaturatedServer(header, resource, err)
	}

	if d.IsDir() {
		// directory edge case is simply passed on to the standard library
		return fileData{resource, Directory, nil}
	}

	Debugf("Assets checkResource 100 %s\n", resource)
	return fileData{resource, Continue, d}
}

func removeLeadingSlash(name string) string {
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}
	return name
}

func (a *Assets) chooseResource(header http.Header, req *http.Request) (string, code) {
	resource := path.Drop(req.URL.Path, a.UnwantedPrefixSegments)
	if a.Spa && isSPARequest(resource) {
		resource = "/"
	}
	if strings.HasSuffix(resource, "/") {
		resource += indexPage
	}
	Debugf("Assets chooseResource %s %s %s\n", req.Method, req.URL.Path, resource)

	if a.Spa && strings.HasSuffix(resource, indexPage) {
		header.Set("Cache-Control", "no-store, max-age=0")
	} else if a.MaxAge > 0 {
		params := append(
			a.CacheDirectives,
			fmt.Sprintf("max-age=%d", a.MaxAge/time.Second),
		)
		header.Set("Cache-Control", strings.Join(params, ", "))
	}
	acceptEncoding := commaSeparatedList(req.Header.Get("Accept-Encoding"))
	if acceptEncoding.Contains("br") {
		brotli := resource + ".br"

		fdbr := a.checkResource(brotli, header)

		if fdbr.code == Continue {
			ext := filepath.Ext(resource)
			header.Set("Content-Type", mime.TypeByExtension(ext))
			// the standard library sometimes overrides the content type via sniffing
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Content-Encoding", "br")
			header.Add("Vary", "Accept-Encoding")
			// weak etag because the representation is not the original file but a compressed variant
			header.Set("ETag", "W/"+calculateEtag(fdbr.fi))
			return brotli, Continue
		}
	}

	if acceptEncoding.Contains("gzip") {
		gzipped := resource + ".gz"

		fdgz := a.checkResource(gzipped, header)

		if fdgz.code == Continue {
			ext := filepath.Ext(resource)
			header.Set("Content-Type", mime.TypeByExtension(ext))
			// the standard library sometimes overrides the content type via sniffing
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Content-Encoding", "gzip")
			header.Add("Vary", "Accept-Encoding")
			// weak etag because the representation is not the original file but a compressed variant
			header.Set("ETag", "W/"+calculateEtag(fdgz.fi))
			return gzipped, Continue
		}
	}

	// no intervention; the file will be served normally by the standard api
	fd := a.checkResource(resource, header)

	if fd.code > 0 {
		// strong etag because the representation is the original file
		header.Set("ETag", calculateEtag(fd.fi))
	}

	return fd.resource, fd.code
}

// ServeHTTP implements the http.Handler interface. Note that it (a) handles
// headers for compression, expiry etc, and then (b) calls the standard
// http.ServeHTTP handler for each request. This ensures that it follows
// all the standard logic paths implemented there, including conditional
// requests and content negotiation.
func (a *Assets) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	original := req.URL.Path
	if strings.HasSuffix(original, "index.html") {
		a.server.ServeHTTP(w, req)
		return
	}
	resource, code := a.chooseResource(w.Header(), req)

	if code == NotFound && a.NotFound != nil {
		// use the provided not-found handler
		Debugf("Assets ServeHTTP (not found) %s %s R:%+v W:%+v\n", req.Method, req.URL.Path, req.Header, w.Header())

		// ww has silently dropped the headers and body from the built-in handler in this case,
		// so complete the response using the original handler.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		a.NotFound.ServeHTTP(w, req)
		return
	}

	if code >= 400 {
		Debugf("Assets ServeHTTP (error %d) %s %s R:%+v W:%+v\n", code, req.Method, req.URL.Path, req.Header, w.Header())
		Debugf("Assets ServeHTTP (error %d) %s %s R:%+v W:%+v\n", code, req.Method, req.URL.Path, req.Header, w.Header())
		http.Error(w, code.String(), int(code))
		return
	}

	req.URL.Path = strings.TrimSuffix(resource, "index.html")

	// Conditional requests and content negotiation are handled in the standard net/http API.
	// Note that req.URL remains unchanged, even if prefix stripping is turned on, because the resource is
	// the only value that matters.
	Debugf("Assets ServeHTTP (ok %d) %s %s (was %s) R:%+v W:%+v\n", code, req.Method, req.URL.Path, original, req.Header, w.Header())
	a.server.ServeHTTP(w, req)

	// leave the path as we found it, in case middleware depends on the original value
	req.URL.Path = original
}

//-------------------------------------------------------------------------------------------------

// Printer is something that allows formatted printing. This is only used for diagnostics.
type Printer func(format string, v ...interface{})

// Debugf is a function that allows diagnostics to be emitted. By default it does very
// little and has almost no impact. Set it to some other function (e.g. using log.Printf) to
// see the diagnostics.
var Debugf Printer = func(format string, v ...interface{}) {}

// example (paste this into setup code elsewhere)
//var servefiles.Debugf Printer = log.Printf
