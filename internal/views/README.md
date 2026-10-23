# templ source

`pages.templ` is the editable source. The `*_templ.go` files are generated using the pinned templ CLI (v0.3.1020) during CI and container builds; they are not hand-edited. `Document`, `Feedback`, `AuthForm`, `StatCard` and `ActivityPanel` are shared components.

`web/styles/base.css` is compiled with Tailwind 4.3.3 and scans templ source. Browser JS and htmx are self-hosted in the compiled binary using `go:embed`. No npm server or runtime CDN is needed.
