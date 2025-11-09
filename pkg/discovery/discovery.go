package discovery

import (
	"fmt"
	"net"
	"regexp"

	"github.com/grandcat/zeroconf"
)

// GetUserName extracts the user= value from the zeroconf TXT records.
func GetUserName(entry *zeroconf.ServiceEntry) (string, error) {
	var reg = regexp.MustCompile("(\\w+)=(\\w+)")
	for _, val := range entry.Text {
		data := reg.FindAllStringSubmatch(val, -1)
		if len(data) < 1 || len(data[0]) != 3 {
			continue
		}
		if data[0][1] == "user" {
			return data[0][2], nil
		}
	}
	return "", fmt.Errorf("User key/value pair not found")
}

// FindMatchingIP returns the first IP from ips that is present on a local interface.
func FindMatchingIP(ips []net.IP) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, ifaceAddr := range ifaceAddrs {
			_, ifaceNet, err := net.ParseCIDR(ifaceAddr.String())
			if err != nil {
				continue
			}
			for _, ip := range ips {
				if ifaceNet.Contains(ip) {
					return ip.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("Found no matching interface")
}
