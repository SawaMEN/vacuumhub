package webassets

import "embed"

// Files are embedded in the standalone Linux binary. Production does not need
// a separate frontend directory, a reverse proxy, Docker or Node.js.
//
//go:embed *.html *.css *.js
var Files embed.FS
