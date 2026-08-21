//go:build !windows

package secrets

func openDPAPIStore(_ string) (Store, error) { return nil, ErrUnsupported }
