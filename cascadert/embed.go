package cascadert

import "embed"

// Source embeds this package's own .go files so cmd/cascade can copy them
// into each build's disposable scratch module (see cmd/cascade/build.go's
// writeCascadert, mirroring Seed's identical seedrt/embed.go). That's
// what lets a compiled Cascade program resolve `import "cascadert"`
// without ever needing network access or a real module dependency on
// this repository: the "dependency" is just a handful of files written
// alongside the generated code.
//
//go:embed *.go
var Source embed.FS
