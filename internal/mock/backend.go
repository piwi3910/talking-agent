// Package mock implements stateful enterprise services. Only this package knows industry data.
package mock

import (
	"encoding/json"
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type User struct {
	tools.Record
	Scenario     string  `json:"scenario"`
	PlanID       string  `json:"plan_id,omitempty"`
	Network      string  `json:"network,omitempty"`
	Router       string  `json:"router,omitempty"`
	Interference bool    `json:"interference,omitempty"`
	Channel      string  `json:"channel,omitempty"`
	UsedGB       float64 `json:"used_gb,omitempty"`
	AllowanceGB  float64 `json:"allowance_gb,omitempty"`
	Invoice      float64 `json:"invoice,omitempty"`
	Balance      float64 `json:"balance,omitempty"`
}
type Backend struct {
	mu       sync.Mutex
	users    map[string]map[string]*User
	catalogs map[string][]tools.Record
	records  map[string][]tools.Record
	serial   int
	// LatencyScale multiplies the simulated per-tool duration served by Handler;
	// 0 (the zero value) answers immediately.
	LatencyScale float64
}

func Load(root string, now time.Time) (*Backend, error) {
	b := &Backend{users: map[string]map[string]*User{}, catalogs: map[string][]tools.Record{}, records: map[string][]tools.Record{}}
	for industry, file := range map[string]string{"telecom": "customers", "school": "families", "aquila": "contacts", "assistant": "people"} {
		var users []User
		if err := read(filepath.Join(root, industry, file+".json"), &users); err != nil {
			return nil, err
		}
		b.users[industry] = map[string]*User{}
		for i := range users {
			u := users[i]
			b.users[industry][u.ID] = &u
		}
	}
	// Adversarial-only identities are routable by ID for isolated suite sessions,
	// but never appear in the CRM's ordinary identity selector.
	for _, u := range []User{
		{Record: tools.Record{ID: "ADV-AQUILA-ADMISSIONS-001", Kind: "contact", Name: "Nadia Rahman"}, Scenario: "Prospective family; child considering Year 5; first enquiry."},
		{Record: tools.Record{ID: "ADV-AQUILA-ADMISSIONS-002", Kind: "contact", Name: "Omar Haddad"}, Scenario: "Prospective family; child considering Year 3; first enquiry."},
		{Record: tools.Record{ID: "ADV-AQUILA-RECEPTION-001", Kind: "contact", Name: "Layla Mansour"}, Scenario: "Prospective family; child considering FS2; first enquiry."},
		{Record: tools.Record{ID: "ADV-AQUILA-RECEPTION-002", Kind: "contact", Name: "Yusuf Karim"}, Scenario: "Prospective family; child considering Year 1; first enquiry."},
	} {
		b.users["aquila"][u.ID] = &u
	}
	for _, u := range []User{
		{Record: tools.Record{ID: "ADV-SCHOOL-SERVICES-001", Kind: "family", Name: "Maya Patel"}, Scenario: "Prospective family; interested in the primary program; first enquiry."},
		{Record: tools.Record{ID: "ADV-SCHOOL-SERVICES-002", Kind: "family", Name: "Ethan Brooks"}, Scenario: "Prospective family; interested in the early years program; first enquiry."},
	} {
		b.users["school"][u.ID] = &u
	}
	for _, x := range []struct{ industry, name string }{{"telecom", "plans"}, {"school", "programs"}, {"school", "school_info"}} {
		var r []tools.Record
		if err := read(filepath.Join(root, x.industry, x.name+".json"), &r); err != nil {
			return nil, err
		}
		b.catalogs[x.name] = r
	}
	for id := range b.users["telecom"] {
		b.records["tickets"] = append(b.records["tickets"], tools.Record{ID: "T-" + id, Kind: "ticket", UserID: id, Status: "closed", Description: "Previous service check completed."})
	}
	b.records["tickets"][0].Description = "Previous service check completed."
	for i := range b.records["tickets"] {
		if b.records["tickets"][i].UserID == "C001" {
			b.records["tickets"][i].Description = "Upstairs Wi-Fi interference resolved temporarily by changing channel to 6."
		}
	}
	// Technician visits run Monday to Saturday in a morning window (9 am) and
	// an afternoon window (1 pm), customer local time. IDs stay TECH-nn.
	day := localMidnight(now, telecomZone)
	for n, offset := 1, 1; n <= 12; offset++ {
		date := day.AddDate(0, 0, offset)
		if date.Weekday() == time.Sunday {
			continue
		}
		for _, hour := range []int{9, 13} {
			at := time.Date(date.Year(), date.Month(), date.Day(), hour, 0, 0, 0, telecomZone)
			slot := slotRecord(fmt.Sprintf("TECH-%02d", n), "technician_slot", "Technician visit", at, "")
			b.records["technician_slots"] = append(b.records["technician_slots"], slot)
			n++
		}
	}
	b.seedSchool(now)
	b.seedAquila(now)
	return b, nil
}
func read(path string, v any) error {
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, v)
}
func (b *Backend) Users(industry string) []tools.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []tools.Record{}
	for _, u := range b.users[industry] {
		if strings.HasPrefix(u.ID, "ADV-") {
			continue
		}
		r := u.Record
		r.Description = u.Scenario
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (b *Backend) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /users/{industry}", func(w http.ResponseWriter, r *http.Request) { write(w, b.Users(r.PathValue("industry"))) })
	m.HandleFunc("POST /tools/execute", func(w http.ResponseWriter, r *http.Request) {
		var req tools.Request
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			w.WriteHeader(400)
			write(w, tools.Failure("invalid_input", "Invalid tool request"))
			return
		}
		if err := b.wait(r.Context(), req.Name); err != nil {
			return
		}
		out := b.Execute(req)
		if out.Error != nil {
			w.WriteHeader(422)
		}
		write(w, out)
	})
	return m
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func result(summary string, records ...tools.Record) tools.Result {
	if records == nil {
		records = []tools.Record{}
	}
	return tools.Result{Summary: summary, Records: records}
}
func owned(rs []tools.Record, user string) []tools.Record {
	out := []tools.Record{}
	for _, r := range rs {
		if r.UserID == user {
			out = append(out, r)
		}
	}
	return out
}
func find(rs []tools.Record, id string) (tools.Record, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return tools.Record{}, false
}
func filter(rs []tools.Record, q string) []tools.Record {
	out := []tools.Record{}
	for _, r := range rs {
		if strings.Contains(strings.ToLower(r.Name+" "+r.Specialty+" "+r.Description+" "+r.Location), strings.ToLower(q)) {
			out = append(out, r)
		}
	}
	return out
}
func (b *Backend) Execute(req tools.Request) tools.Result {
	b.mu.Lock()
	defer b.mu.Unlock()
	u := b.users[req.Industry][req.UserID]
	if u == nil {
		return tools.Failure("identity_not_found", "Selected identity does not belong to this industry")
	}
	a := req.Arguments
	if req.Industry == "telecom" {
		return b.telecom(u, req.Name, a)
	}
	if req.Industry == "school" {
		return b.school(u, req.Name, a)
	}
	if req.Industry == "aquila" {
		return b.aquila(u, req.Name, a)
	}
	return tools.Failure("unknown_industry", "Unknown backend industry")
}
func (b *Backend) telecom(u *User, name string, a map[string]string) tools.Result {
	switch name {
	case "customer.lookup", "customer.profile":
		return result("Customer account identified.", u.Record)
	case "customer.authenticate":
		return result("Trusted demo identity selected by operator; this is not production authentication.", tools.Record{ID: u.ID, Kind: "identity", Status: "demo_authenticated"})
	case "subscription.list", "subscription.details":
		if a["subscription_id"] != "" && a["subscription_id"] != "SUB-"+u.ID {
			return tools.Failure("not_found", "Subscription not found")
		}
		p, _ := find(b.catalogs["plans"], u.PlanID)
		return result("Current subscription.", tools.Record{ID: "SUB-" + u.ID, Kind: "subscription", UserID: u.ID, RelatedID: p.ID, Name: p.Name, Status: u.Status, Amount: p.Amount, Currency: "USD"})
	case "billing.balance":
		return result(fmt.Sprintf("Outstanding balance is $%.2f.", u.Balance), tools.Record{ID: "BAL-" + u.ID, Kind: "balance", Amount: u.Balance, Currency: "USD"})
	case "billing.invoice", "billing.explain":
		rs := []tools.Record{{ID: "base", Kind: "invoice_item", Name: "Fiber service", Amount: 50, Currency: "USD"}}
		summary := fmt.Sprintf("Current invoice $%.2f; previous invoice $50.00. Outstanding balance $%.2f.", u.Invoice, u.Balance)
		if u.Invoice > 50 {
			rs = append(rs, tools.Record{ID: "roaming", Kind: "invoice_item", Name: "International roaming", Amount: 35, Currency: "USD"}, tools.Record{ID: "installation", Kind: "invoice_item", Name: "One-time equipment installation", Amount: 20, Currency: "USD"})
			summary += " The $55 increase is $35 roaming plus a $20 one-time installation charge."
		}
		if u.Balance > 0 {
			summary += " The invoice is overdue and service is restricted. Please settle it through the billing portal; restarting the router will not restore service."
		}
		return result(summary, rs...)
	case "usage.current":
		status := "normal"
		if u.UsedGB >= u.AllowanceGB {
			status = "throttled"
		}
		return result(fmt.Sprintf("Mobile usage %.0f / %.0f GB; status %s.", u.UsedGB, u.AllowanceGB, status), tools.Record{ID: "usage", Kind: "usage", Status: status, Value: u.UsedGB, Unit: "GB", Description: fmt.Sprintf("Allowance %.0f GB", u.AllowanceGB)})
	case "usage.history":
		return result("Previous three monthly mobile usage totals: 11 GB, 14 GB, 18 GB.", tools.Record{ID: "month-1", Kind: "usage", Value: 11, Unit: "GB"}, tools.Record{ID: "month-2", Kind: "usage", Value: 14, Unit: "GB"}, tools.Record{ID: "month-3", Kind: "usage", Value: 18, Unit: "GB"})
	case "network.outages":
		return b.telecomOutage(u)
	case "network.status":
		return b.telecomNetwork(u, false)
	case "network.diagnostics":
		return b.telecomNetwork(u, true)
	case "network.speedtest":
		return b.telecomSpeedtest(u)
	case "wifi.status", "wifi.diagnostics":
		return b.telecomWifi(u)
	case "wifi.devices":
		laptop := map[bool]string{true: "weak signal", false: "strong signal"}[u.Interference]
		return result("Three devices are connected."+tools.GuidanceMarker+"A weak-signal device on a busy channel points to interference; check wifi.diagnostics.", tools.Record{ID: "DEV-1", Kind: "device", Name: "Living room TV", Status: "strong signal", Description: "5 GHz, 8 metres from the router"}, tools.Record{ID: "DEV-2", Kind: "device", Name: "Upstairs laptop", Status: laptop, Description: "Channel " + u.Channel}, tools.Record{ID: "DEV-3", Kind: "device", Name: "Mobile phone", Status: "strong signal", Description: "5 GHz"})
	case "wifi.restart", "wifi.optimize":
		if u.Network != "online" {
			return tools.Failure("action_blocked", "Equipment changes cannot resolve "+u.Network+". Resolve the access issue first.")
		}
		if name == "wifi.restart" {
			u.Router = "healthy"
			l := b.lineFor(u)
			secs := 38 + int(pick(u.ID, "boot", 0)*30)
			return result(fmt.Sprintf("Router restarted successfully. It was back online after about %d seconds, the line re-synced at %.0f megabits and packet loss is down to %.1f percent. Router health is now normal.", secs, l.sync, l.loss))
		}
		ch := a["channel"]
		if ch != "1" && ch != "6" && ch != "11" {
			return tools.Failure("invalid_input", "Channel must be 1, 6 or 11")
		}
		u.Channel = ch
		u.Interference = ch == "6"
		if u.Interference {
			return result("Wi-Fi channel changed to " + ch + ". Interference remains on this channel; channel 11 is usually the quietest here.")
		}
		before, after := -between(pick(u.ID, "rssi", 1), 74, 80), -between(pick(u.ID, "rssi", 2), 54, 62)
		return result(fmt.Sprintf("Wi-Fi channel changed to %s. Interference cleared; upstairs signal improved from %.0f to %.0f dBm and the channel is now only %d percent busy.", ch, before, after, int(between(pick(u.ID, "util", 2), 10, 25))))
	case "ticket.list":
		return result("Your support tickets.", owned(b.records["tickets"], u.ID)...)
	case "ticket.details":
		r, ok := find(owned(b.records["tickets"], u.ID), a["ticket_id"])
		if !ok {
			return tools.Failure("not_found", "Ticket not found")
		}
		return result("Ticket details.", r)
	case "ticket.create":
		res := b.create("tickets", "ticket", u.ID, a["summary"])
		if res.Error == nil && len(res.Records) > 0 {
			res.Summary = "Request created. Support ticket " + res.Records[0].ID + " is open; a specialist replies within four business hours by text message and email."
		}
		return res
	case "ticket.update":
		if a["status"] != "open" && a["status"] != "closed" {
			return tools.Failure("invalid_input", "Status must be open or closed")
		}
		for i, r := range b.records["tickets"] {
			if r.ID == a["ticket_id"] && r.UserID == u.ID {
				b.records["tickets"][i].Status = a["status"]
				return result("Ticket updated.", b.records["tickets"][i])
			}
		}
		return tools.Failure("not_found", "Ticket not found")
	case "plan.list", "plan.compare":
		return result("Available plans below. Current plan: "+u.PlanID+". Fiber upgrades increase broadband speed; Mobile 50 GB increases the mobile allowance. No other offers are available.", b.catalogs["plans"]...)
	case "plan.change":
		p, ok := find(b.catalogs["plans"], a["plan_id"])
		if !ok {
			return tools.Failure("not_found", "Plan not found")
		}
		u.PlanID = p.ID
		if p.ID == "mobile-50" {
			u.AllowanceGB = 50
		}
		return result("Plan changed successfully to "+p.Name+".", p)
	case "technician.availability":
		out := []tools.Record{}
		for _, r := range b.records["technician_slots"] {
			if r.Status == "available" && slotFuture(r, time.Now()) {
				out = append(out, r)
			}
		}
		return result(availabilitySummary("Available technician visits.", out, nil), out...)
	case "technician.book":
		for i, r := range b.records["technician_slots"] {
			if r.ID == a["slot_id"] && r.Status == "available" && slotFuture(r, time.Now()) {
				r.Status = "booked"
				r.UserID = u.ID
				b.records["technician_slots"][i] = r
				return result("Technician visit booked for "+whenWords(mustTime(r))+". Your confirmation number is "+strings.Replace(r.ID, "TECH-", "TV-", 1)+"-"+fmt.Sprint(4100+int(pick(u.ID, r.ID, 0)*800))+". A text message confirms it now, and the technician phones about 30 minutes before arriving. Please make sure someone over 18 is home and the router is reachable.", r)
			}
		}
		return tools.Failure("slot_unavailable", "Technician slot unavailable")
	}
	return tools.Failure("unknown_tool", "Tool not available in telecom backend")
}
func (b *Backend) create(collection, kind, user, summary string) tools.Result {
	if strings.TrimSpace(summary) == "" {
		return tools.Failure("invalid_input", "Summary is required")
	}
	b.serial++
	r := tools.Record{ID: fmt.Sprintf("%s-%d", strings.ToUpper(kind), b.serial), Kind: kind, UserID: user, Status: "open", Description: summary}
	b.records[collection] = append(b.records[collection], r)
	return result("Request created.", r)
}
