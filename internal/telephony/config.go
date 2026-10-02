// Package telephony exposes the agents as an ordinary SIP/RTP service for Hello.
package telephony

import (
	"encoding/json"
	"enterprise-ai-demo/internal/config"
	"fmt"
	"io"
	"net"
	"os"
)

type Config struct {
	BindHost     string            `json:"bind_host"`
	Port         int               `json:"port"`
	AdvertiseIP  string            `json:"advertise_ip"`
	RTPStart     int               `json:"rtp_start"`
	RTPEnd       int               `json:"rtp_end"`
	MaxCalls     int               `json:"max_calls"`
	AllowedPeers []string          `json:"allowed_peers"`
	Numbers      map[string]string `json:"numbers"`
	peers        []*net.IPNet
}

func Load(path string, agents map[string]*config.Agent) (Config, error) {
	c := Config{BindHost: "0.0.0.0", Port: 5060, RTPStart: 10000, RTPEnd: 10200, MaxCalls: 32}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("trailing SIP configuration data")
	}
	return c, c.validate(agents)
}
func (c *Config) validate(agents map[string]*config.Agent) error {
	ip := net.ParseIP(c.AdvertiseIP)
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("SIP advertise_ip must be a reachable IP address")
	}
	if net.ParseIP(c.BindHost) == nil || c.Port < 1 || c.Port > 65535 || c.RTPStart < 1024 || c.RTPStart%2 != 0 || c.RTPEnd <= c.RTPStart || c.RTPEnd > 65535 || c.MaxCalls < 1 || c.MaxCalls > 1000 || c.MaxCalls > (c.RTPEnd-c.RTPStart)/2 {
		return fmt.Errorf("invalid SIP bind, port range or call capacity")
	}
	if len(c.AllowedPeers) == 0 {
		return fmt.Errorf("SIP requires allowed PBX peers")
	}
	for n, id := range c.Numbers {
		if n == "" || agents[id] == nil {
			return fmt.Errorf("invalid SIP number route %q", n)
		}
	}
	c.peers = nil
	for _, raw := range c.AllowedPeers {
		_, p, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("invalid SIP peer CIDR %q", raw)
		}
		c.peers = append(c.peers, p)
	}
	return nil
}
func (c Config) allowed(source string) bool {
	host, _, err := net.SplitHostPort(source)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, p := range c.peers {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
