# integration_test

Integration tests for mekami-core. These tests require
`github.com/Wolf258/mekami-core-go` as a test-only dependency
and are gated behind the `integration` build tag. They are NOT
part of the default test suite.

## Running

```
go get github.com/Wolf258/mekami-core-go
go test -tags=integration ./integration_test/...
```

These tests live in the main module (mekami-core) so they can
exercise the ingest pipeline against real Go source code
without crossing module boundaries. They are excluded from
`go test ./...` because the file-level build tag prevents them
from compiling under default tags.
