// Package version holds the build version, set by -ldflags at release.
package version

var Version = "0.0.0-dev"

// Repo is the "owner/name" repository whose releases are checked, set by -ldflags at release. Empty (a
// development build) means no update check ever.
var Repo string
