package telephony

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

type Registration struct {
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	PasswordSaved bool   `json:"password_saved"`
}
type GatewayConfig struct {
	Revision      uint64                  `json:"revision"`
	Host          string                  `json:"host"`
	Port          int                     `json:"port"`
	Transport     string                  `json:"transport"`
	Mode          string                  `json:"mode"`
	AdvertiseIP   string                  `json:"advertise_ip"`
	Registrations map[string]Registration `json:"registrations"`
	AutoConnect   bool                    `json:"auto_connect"`
}
type GatewayStatus struct {
	Config   GatewayConfig `json:"config"`
	State    string        `json:"state"`
	Error    string        `json:"error"`
	SIPPort  int           `json:"sip_port"`
	RTPStart int           `json:"rtp_start"`
	RTPEnd   int           `json:"rtp_end"`
}

// Gateway serializes listener lifecycles; configuration changes never race live calls.
type Gateway struct {
	mu             sync.Mutex
	op             sync.Mutex
	path           string
	root           context.Context
	template       Server
	config         GatewayConfig
	state, problem string
	cancel         context.CancelFunc
	done           chan error
}

func OpenGateway(root context.Context, path, advertise string, template Server) (*Gateway, error) {
	g := &Gateway{path: path, root: root, template: template, state: "disconnected", config: GatewayConfig{Host: "", Port: 5060, Transport: "udp", Mode: "trunk", AdvertiseIP: advertise, Registrations: map[string]Registration{}}}
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &g.config)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return g, nil
}
func (g *Gateway) Snapshot() GatewayStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.config
	c.Registrations = map[string]Registration{}
	for id, r := range g.config.Registrations {
		r.PasswordSaved = r.Password != ""
		r.Password = ""
		c.Registrations[id] = r
	}
	return GatewayStatus{Config: c, State: g.state, Error: g.problem, SIPPort: 5060, RTPStart: 10000, RTPEnd: 10199}
}
func (g *Gateway) setState(state, problem string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = state
	g.problem = problem
}
func (g *Gateway) persist(c GatewayConfig) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(g.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(g.path), ".sip-gateway-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), g.path)
}
func (g *Gateway) Save(c GatewayConfig) error {
	g.op.Lock()
	defer g.op.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		return fmt.Errorf("disconnect before changing gateway settings")
	}
	if c.Revision != g.config.Revision {
		return ErrSettingsConflict
	}
	c.Host = strings.TrimSpace(c.Host)
	if net.ParseIP(c.Host) == nil {
		return fmt.Errorf("gateway address must be an IP address")
	}
	if c.Port < 1 || c.Port > 65535 || (c.Transport != "udp" && c.Transport != "tcp") || (c.Mode != "trunk" && c.Mode != "register") {
		return fmt.Errorf("invalid gateway port, transport or connection mode")
	}
	if ip := net.ParseIP(c.AdvertiseIP); ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("agent advertised address must be a reachable IP")
	}
	c.Registrations = cloneRegistrations(c.Registrations)
	for id, r := range c.Registrations {
		if g.template.Agents[id] == nil {
			return fmt.Errorf("unknown registration persona")
		}
		if r.Username != "" && !phoneNumber.MatchString(r.Username) {
			return fmt.Errorf("registration extension must contain digits with an optional +")
		}
		if len(r.Password) > 1024 {
			return fmt.Errorf("SIP password too long")
		}
		if r.Password == "" && r.PasswordSaved {
			r.Password = g.config.Registrations[id].Password
		}
		r.PasswordSaved = false
		c.Registrations[id] = r
	}
	c.Revision++
	if err := g.persist(c); err != nil {
		return err
	}
	g.config = c
	g.problem = ""
	return nil
}
func cloneRegistrations(in map[string]Registration) map[string]Registration {
	out := map[string]Registration{}
	for id, r := range in {
		out[id] = r
	}
	return out
}
func (g *Gateway) Disconnect() error {
	g.op.Lock()
	defer g.op.Unlock()
	return g.disconnect()
}
func (g *Gateway) disconnect() error {
	g.mu.Lock()
	cancel, done := g.cancel, g.done
	c := g.config
	c.AutoConnect = false
	c.Revision++
	g.mu.Unlock()
	if err := g.persist(c); err != nil {
		return err
	}
	if cancel != nil {
		cancel()
		<-done
	}
	g.mu.Lock()
	g.cancel = nil
	g.done = nil
	g.config = c
	g.state = "disconnected"
	g.problem = ""
	g.mu.Unlock()
	return nil
}
func (g *Gateway) Close() {
	g.op.Lock()
	defer g.op.Unlock()
	g.mu.Lock()
	cancel, done := g.cancel, g.done
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (g *Gateway) Connect() error {
	g.op.Lock()
	defer g.op.Unlock()
	g.mu.Lock()
	if g.cancel != nil {
		g.mu.Unlock()
		return fmt.Errorf("gateway already started; disconnect before reconnecting")
	}
	c := g.config
	g.mu.Unlock()
	ip := net.ParseIP(c.Host)
	if ip == nil {
		return fmt.Errorf("save a gateway IP address first")
	}
	if c.Mode == "register" {
		count := 0
		for id, r := range c.Registrations {
			if r.Username == "" {
				continue
			}
			count++
			a, _ := g.template.Settings.Route(r.Username)
			if a == nil || a.ID != id {
				return fmt.Errorf("registration extension for %s must also be assigned to that persona", id)
			}
		}
		if count == 0 {
			return fmt.Errorf("configure at least one persona registration extension")
		}
	}
	cfg := Config{BindHost: "0.0.0.0", Port: 5060, AdvertiseIP: c.AdvertiseIP, RTPStart: 10000, RTPEnd: 10200, MaxCalls: 32, AllowedPeers: []string{c.Host + "/32"}}
	if ip.To4() == nil {
		cfg.AllowedPeers = []string{c.Host + "/128"}
	}
	for _, peer := range strings.Split(os.Getenv("SIP_TRUSTED_FORWARDERS"), ",") {
		if peer = strings.TrimSpace(peer); peer != "" {
			cfg.AllowedPeers = append(cfg.AllowedPeers, peer)
		}
	}
	if err := cfg.validate(g.template.Agents); err != nil {
		return err
	}
	if !g.template.Speech.Enabled() {
		return fmt.Errorf("phone speech services are unavailable")
	}
	c.AutoConnect = true
	if err := g.persist(c); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(g.root)
	done := make(chan error, 1)
	g.mu.Lock()
	g.config = c
	g.cancel = cancel
	g.done = done
	g.state = "connecting"
	g.problem = ""
	g.mu.Unlock()
	s := &Server{Config: cfg, Settings: g.template.Settings, Agents: g.template.Agents, Sessions: g.template.Sessions, Runtime: g.template.Runtime, Speech: g.template.Speech, Voices: g.template.Voices}
	monitorDone := make(chan struct{})
	monitorStarted := false
	s.Ready = func(ua *sipgo.UserAgent) error {
		client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(c.AdvertiseIP), sipgo.WithClientPort(5060))
		if err != nil {
			return err
		}
		monitorStarted = true
		go func() { defer close(monitorDone); g.monitor(ctx, client, c) }()
		return nil
	}
	s.Stopping = func() {
		if monitorStarted {
			<-monitorDone
		}
	}
	go func() {
		err := s.Run(ctx)
		cancel()
		if monitorStarted {
			<-monitorDone
		}
		if err != nil {
			g.setState("error", err.Error())
		}
		done <- err
	}()
	return nil
}
func (g *Gateway) monitor(ctx context.Context, client *sipgo.Client, c GatewayConfig) {
	requests := map[string]*sip.Request{}
	if c.Mode == "register" {
		for id, r := range c.Registrations {
			if r.Username != "" {
				req := sip.NewRequest(sip.REGISTER, sip.Uri{User: r.Username, Host: c.Host, Port: c.Port})
				req.SetTransport(strings.ToUpper(c.Transport))
				req.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: r.Username, Host: c.AdvertiseIP, Port: 5060, UriParams: sip.HeaderParams{{K: "transport", V: c.Transport}}}})
				requests[id] = req
			}
		}
	}

	defer func() {
		if c.Mode == "register" {
			for id, req := range requests {
				req.RemoveHeader("Expires")
				req.AppendHeader(sip.NewHeader("Expires", "0"))
				_ = gatewayRequest(context.Background(), client, req, c.Registrations[id], true)
			}
		}
	}()
	for {
		delay := 30 * time.Second
		var failures []error
		if c.Mode == "trunk" {
			req := sip.NewRequest(sip.OPTIONS, sip.Uri{Host: c.Host, Port: c.Port})
			req.SetTransport(strings.ToUpper(c.Transport))
			if err := gatewayRequest(ctx, client, req, Registration{}, false); err != nil {
				failures = append(failures, err)
			}
		} else {
			for id, req := range requests {
				req.RemoveHeader("Expires")
				req.AppendHeader(sip.NewHeader("Expires", "3600"))
				res, err := gatewayExchange(ctx, client, req, c.Registrations[id], true)
				if err != nil {
					failures = append(failures, fmt.Errorf("persona %s: %w", id, err))
					continue
				}
				expiry := 3600
				if h := res.GetHeader("Expires"); h != nil {
					if n, e := strconv.Atoi(h.Value()); e == nil {
						expiry = n
					}
				}
				if h := res.Contact(); h != nil {
					if raw, ok := h.Params.Get("expires"); ok {
						if n, e := strconv.Atoi(raw); e == nil {
							expiry = n
						}
					}
				}
				if expiry <= 0 {
					failures = append(failures, fmt.Errorf("persona %s: gateway returned an expired registration", id))
					continue
				}
				refresh := time.Duration(expiry) * time.Second / 2
				if refresh < time.Second {
					refresh = time.Second
				}
				if refresh < delay {
					delay = refresh
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		if len(failures) > 0 {
			g.setState("error", errors.Join(failures...).Error())
			delay = 5 * time.Second
		} else {
			g.setState("connected", "")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func gatewayRequest(parent context.Context, client *sipgo.Client, req *sip.Request, r Registration, register bool) error {
	_, err := gatewayExchange(parent, client, req, r, register)
	return err
}
func gatewayExchange(parent context.Context, client *sipgo.Client, req *sip.Request, r Registration, register bool) (*sip.Response, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var opts []sipgo.ClientRequestOption
	if register {
		req.RemoveHeader("Via")
		req.RemoveHeader("Authorization")
		req.RemoveHeader("Proxy-Authorization")
		opts = append(opts, sipgo.ClientRequestRegisterBuild)
	}
	res, err := client.Do(ctx, req, opts...)
	if err != nil {
		return nil, fmt.Errorf("gateway did not respond: check IP, port, transport and firewall")
	}
	if register && res.StatusCode == 423 {
		if h := res.GetHeader("Min-Expires"); h != nil {
			n, e := strconv.Atoi(h.Value())
			if e == nil && n > 0 && n <= 86400 {
				req.RemoveHeader("Expires")
				req.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(n)))
				req.RemoveHeader("Via")
				res, err = client.Do(ctx, req, opts...)
				if err != nil {
					return nil, fmt.Errorf("gateway registration retry failed")
				}
			}
		}
	}
	if (res.StatusCode == 401 || res.StatusCode == 407) && r.Username != "" {
		res, err = client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: r.Username, Password: r.Password})
		if err != nil {
			return nil, fmt.Errorf("gateway authentication failed")
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway returned SIP %d", res.StatusCode)
	}
	return res, nil
}
