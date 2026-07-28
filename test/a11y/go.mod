// A module of its own, deliberately.
//
// This suite drives a real browser, which is the only way to check computed
// styles, focus order and what axe-core sees. None of that belongs in the
// dependency graph of a static binary, and a `require` here can never reach the
// production build. It needs nothing but the standard library and a Chrome.
module github.com/centre-for-dpi/whitelabel-dpi-dashboard/test/a11y

go 1.26.5
