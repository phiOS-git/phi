// Package build holds metadata baked into the binary at compile time.
package build

// Version is overwritten at build time via:
//
//	go build -ldflags "-X phi/internal/build.Version=1.2.3"
//
// phi-packages (S-11) sets it from the PKGBUILD version. Local builds keep
// the default below.
var Version = "dev"
