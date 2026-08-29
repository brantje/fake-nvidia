// Package compatibility contains the native, CPU-only fake NVIDIA integration
// tests. The executable tests remain behind the integration build tag; this
// untagged package declaration lets default lint and typecheck tools load the
// package without changing which tests run.
package compatibility
