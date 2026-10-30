// Package load reimplements the MultiPageLoader orchestration layer: URL
// guessing, HTTP(S)/file fetching, cookies, proxy, auth, local ACL, and POST
// bodies. It is not a browser: it hands raw bytes to the HTML/CSS/layout
// pipeline. JavaScript is never executed.
package load
