package layouts

// robotsMode is how a page presents itself to search engines. It is a closed
// vocabulary of three; seoHead's doc comment says which layout uses which and why.
// Pages never pick it directly: they pick a layout (Base, BaseNoIndex, BaseAuth).
type robotsMode int

const (
	robotsIndex   robotsMode = iota // public and ranked: full share metadata, index
	robotsNoIndex                   // public, not ranked: full share metadata, noindex
	robotsPrivate                   // behind a session: noindex, nofollow, nothing else
)
