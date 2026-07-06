module github.com/wago-org/wasi

go 1.22

require github.com/wago-org/wago v0.1.0

// The wago engine is not yet published with a version tag, so build against the
// sibling checkout. Remove this replace once github.com/wago-org/wago has a
// tagged release and bump the require above to it.
replace github.com/wago-org/wago => ../wago
