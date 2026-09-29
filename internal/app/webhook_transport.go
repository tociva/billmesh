package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

var webhookBlockedRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
}

type webhookResolver func(context.Context, string) ([]net.IPAddr, error)

func webhookPrivateHostAllowed(host string) bool {
	for _, allowed := range strings.Split(os.Getenv("WEBHOOK_ALLOWED_PRIVATE_HOSTS"), ",") {
		if strings.EqualFold(strings.TrimSpace(allowed), host) && host != "" {
			return true
		}
	}
	return false
}

func blockedWebhookIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return true
	}
	for _, prefix := range webhookBlockedRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func resolveWebhookHost(ctx context.Context, host string, lookup webhookResolver) (net.IP, error) {
	if host == "" {
		return nil, errors.New("empty webhook host")
	}
	var addresses []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		addresses = []net.IPAddr{{IP: literal}}
	} else {
		var err error
		addresses, err = lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve webhook host: %w", err)
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("webhook host has no addresses")
	}
	if !webhookPrivateHostAllowed(host) {
		for _, address := range addresses {
			if blockedWebhookIP(address.IP) {
				return nil, errors.New("webhook host resolves to a prohibited address")
			}
		}
	}
	return addresses[0].IP, nil
}

func validateWebhookTarget(ctx context.Context, target string, lookup webhookResolver) error {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("target_url must be an absolute HTTP(S) URL without credentials")
	}
	_, err = resolveWebhookHost(ctx, parsed.Hostname(), lookup)
	return err
}

func newWebhookClient(lookup webhookResolver, dialer *net.Dialer) *http.Client {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 5 * time.Second}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip, err := resolveWebhookHost(ctx, host, lookup)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many webhook redirects")
			}
			return validateWebhookTarget(req.Context(), req.URL.String(), lookup)
		},
	}
}
