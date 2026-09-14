// Package query defines the vendor-neutral query.Expr AST, the backend
// compiler interface (AST → LogsQL), and query limits. All query-string
// construction and escaping happens here. See docs/architecture.md §2.3
// and docs/security.md §5.
package query
