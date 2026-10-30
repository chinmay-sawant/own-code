package load

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// checkResponseHeaderLimits bounds headers on a received response. It runs
// for the default transport and for a caller-supplied client. The default
// transport also sets MaxResponseHeaderBytes so the bytes are refused while
// they are still being read.
func checkResponseHeaderLimits(header http.Header) error {
	if len(header) > MaxResponseHeaders {
		return fmt.Errorf("%w: %d response headers, limit %d",
			errMetadataTooLarge, len(header), MaxResponseHeaders)
	}

	total := 0
	for key, values := range header {
		total += len(key)

		for _, value := range values {
			total += len(value)
		}
	}

	if total > MaxResponseHeaderBytes {
		return fmt.Errorf("%w: %d response header bytes, limit %d",
			errMetadataTooLarge, total, MaxResponseHeaderBytes)
	}

	return nil
}

// stripIPv6Zone removes a zone id so [fe80::1%eth0] and fe80::1%25eth0
// compare as the same address. Bracketed hosts keep their brackets and
// any port. A bare host is the form URL.Hostname returns.
func stripIPv6Zone(host string) string {
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return host
		}

		inside := host[1:end]
		if zone := strings.IndexByte(inside, '%'); zone >= 0 {
			inside = inside[:zone]
		}

		return "[" + inside + "]" + host[end+1:]
	}

	// URL.Hostname drops the brackets but keeps the zone. Only treat a
	// percent as a zone when the host is an IPv6 literal (it contains a colon).
	if zone := strings.IndexByte(host, '%'); zone >= 0 && strings.Contains(host, ":") {
		return host[:zone]
	}

	return host
}

// blockedSpecialIP reports addresses outside IsPrivate that still must not
// be dialed in restricted mode: multicast, the limited broadcast, the
// protocol-assignment host, and the Azure WireServer.
func blockedSpecialIP(addr net.IP) bool {
	return addr.IsMulticast() || addr.Equal(limitedBroadcastIP) ||
		addr.Equal(protocolAssignIP) || addr.Equal(azureWireServerIP)
}

// policyErrorWithoutLocation peels url.Error wrappers off a network-policy
// failure. The inner error is the one CheckRedirect or the dial policy
// returned. Its text does not include the redirect Location.
func policyErrorWithoutLocation(err error) error {
	if err == nil || !errors.Is(err, ErrNetworkPolicy) {
		return nil
	}

	for {
		var locationErr *url.Error
		if !errors.As(err, &locationErr) || locationErr == nil || locationErr.Err == nil {
			return err
		}

		next := locationErr.Err
		if errors.Is(next, err) {
			return err
		}

		err = next
	}
}
