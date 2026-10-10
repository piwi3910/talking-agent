package telephony

import (
	"context"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/speech"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestGatewayPersistenceAndCredentials(t *testing.T) {
	agents := map[string]*config.Agent{"a": {ID: "a"}, "b": {ID: "b"}}
	path := filepath.Join(t.TempDir(), "gateway.json")
	g, err := OpenGateway(context.Background(), path, "127.0.0.1", &Server{Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	c := g.Snapshot().Config
	c.Host = "127.0.0.1"
	c.Mode = "register"
	c.Registrations = map[string]Registration{"a": {Username: "500", Password: "test-secret"}, "b": {Username: "501", Password: "other-secret"}}
	if err = g.Save(c); err != nil {
		t.Fatal(err)
	}
	snap := g.Snapshot()
	if snap.Config.Registrations["a"].Password != "" || !snap.Config.Registrations["a"].PasswordSaved {
		t.Fatal("saved credential exposed")
	}
	if err = g.Save(c); err != ErrSettingsConflict {
		t.Fatalf("stale save %v", err)
	}
	if err = g.Save(snap.Config); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenGateway(context.Background(), path, "", &Server{Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	if restored.config.Registrations["a"].Password != "test-secret" {
		t.Fatal("password lost on masked save")
	}
	c = restored.Snapshot().Config
	c.Registrations["a"] = Registration{Username: "500"}
	if err = restored.Save(c); err != nil {
		t.Fatal(err)
	}
	if restored.config.Registrations["a"].Password != "" {
		t.Fatal("password could not be cleared")
	}
}
func TestGatewaySIPProbeAndConcurrentRegistrations(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ua, _ := sipgo.NewUA()
	defer ua.Close()
	srv, _ := sipgo.NewServer(ua)
	received := make(chan string, 10)
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		received <- req.To().Address.User
		_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})
	go func() { _ = srv.ServeUDP(conn) }()
	defer conn.Close()
	clientUA, _ := sipgo.NewUA()
	defer clientUA.Close()
	client, _ := sipgo.NewClient(clientUA, sipgo.WithClientHostname("127.0.0.1"))
	req := sip.NewRequest(sip.OPTIONS, sip.Uri{Host: "127.0.0.1", Port: port})
	req.SetTransport("UDP")
	if err = gatewayRequest(ctx, client, req, Registration{}, false); err != nil {
		t.Fatal(err)
	}
	for _, number := range []string{"500", "501"} {
		req := sip.NewRequest(sip.REGISTER, sip.Uri{User: number, Host: "127.0.0.1", Port: port})
		req.SetTransport("UDP")
		req.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: number, Host: "127.0.0.1", Port: 5060}})
		req.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(60)))
		if err = gatewayRequest(ctx, client, req, Registration{Username: number}, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range []string{"500", "501"} {
		select {
		case got := <-received:
			if got != expected {
				t.Fatalf("registration %s expected %s", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("registration not received")
		}
	}
}

func TestGatewayLifecycle(t *testing.T) {
	setTestSIPDigest(t, "agent.test", map[string]sipDigestUser{"handset-a": {HA1: testSIPHA1("handset-a", "agent.test", "fixture-a"), Persona: "a"}})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ua, _ := sipgo.NewUA()
	defer ua.Close()
	srv, _ := sipgo.NewServer(ua)
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})
	go func() { _ = srv.ServeUDP(conn) }()
	agents := map[string]*config.Agent{"a": {ID: "a"}}
	settings, _ := OpenSettings(filepath.Join(t.TempDir(), "phones.json"), agents, nil)
	g, err := OpenGateway(context.Background(), filepath.Join(t.TempDir(), "gateway.json"), "127.0.0.1", &Server{Agents: agents, Settings: settings, Speech: &speech.Client{STTURL: "http://localhost", TTSURL: "http://localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.Snapshot().Config
	c.Host = "127.0.0.1"
	c.Port = conn.LocalAddr().(*net.UDPAddr).Port
	if err = g.Save(c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = g.Connect(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for g.Snapshot().State != "connected" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if snap := g.Snapshot(); snap.State != "connected" {
			t.Fatalf("connection failed: %+v", snap)
		}
		if !settings.Snapshot().Enabled {
			t.Fatal("listener missing")
		}
		if err = g.Save(g.Snapshot().Config); err == nil {
			t.Fatal("changed live gateway")
		}
		if err = g.Disconnect(); err != nil {
			t.Fatal(err)
		}
		if settings.Snapshot().Enabled || g.Snapshot().Config.AutoConnect {
			t.Fatal("disconnect left listeners/reconnect enabled")
		}
	}
}

func TestGatewayDigestAuthentication(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ua, _ := sipgo.NewUA()
	defer ua.Close()
	srv, _ := sipgo.NewServer(ua)
	challenge := &digest.Challenge{Realm: "hello", Nonce: "test-nonce", Algorithm: "MD5"}
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		header := req.GetHeader("Authorization")
		if header == nil {
			res := sip.NewResponseFromRequest(req, 401, "Unauthorized", nil)
			res.AppendHeader(sip.NewHeader("WWW-Authenticate", challenge.String()))
			_ = tx.Respond(res)
			return
		}
		creds, e := digest.ParseCredentials(header.Value())
		if e != nil {
			t.Error(e)
			return
		}
		expected, _ := digest.Digest(challenge, digest.Options{Method: "REGISTER", URI: creds.URI, Username: "500", Password: "secret"})
		code := 200
		if creds.Response != expected.Response {
			code = 403
		}
		_ = tx.Respond(sip.NewResponseFromRequest(req, code, "Result", nil))
	})
	go func() { _ = srv.ServeUDP(conn) }()
	clientUA, _ := sipgo.NewUA()
	defer clientUA.Close()
	client, _ := sipgo.NewClient(clientUA, sipgo.WithClientHostname("127.0.0.1"))
	for _, password := range []string{"secret", "wrong"} {
		req := sip.NewRequest(sip.REGISTER, sip.Uri{User: "500", Host: "127.0.0.1", Port: conn.LocalAddr().(*net.UDPAddr).Port})
		req.SetTransport("UDP")
		req.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: "500", Host: "127.0.0.1", Port: 5060}})
		err = gatewayRequest(context.Background(), client, req, Registration{Username: "500", Password: password}, true)
		if (err == nil) != (password == "secret") {
			t.Fatalf("digest password result: %v", err)
		}
	}
}
