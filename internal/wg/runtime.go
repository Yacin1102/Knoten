package wg

// runtime.go is for the platform-independent side of applying the tunnel:
// turning the state from state.go into a live WireGuard device through wgctrl,
// instead of rendering a wg-quick config file (config.go, to be removed).


