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
	for _, x := range []struct{ industry, name string }{{"telecom", "plans"}, {"school", "programs"}, {"school", "school_info"}} {
		var r []tools.Record
		if err := read(filepath.Join(root, x.industry, x.name+".json"), &r); err != nil {
			return nil, err
		}
		b.catalogs[x.name] = r
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for id := range b.users["telecom"] {
		b.records["tickets"] = append(b.records["tickets"], tools.Record{ID: "T-" + id, Kind: "ticket", UserID: id, Status: "closed", Description: "Previous service check completed."})
	}
	b.records["tickets"][0].Description = "Previous service check completed."
	for i := range b.records["tickets"] {
		if b.records["tickets"][i].UserID == "C001" {
			b.records["tickets"][i].Description = "Upstairs Wi-Fi interference resolved temporarily by changing channel to 6."
		}
	}
	for i := 1; i <= 8; i++ {
		b.records["technician_slots"] = append(b.records["technician_slots"], tools.Record{ID: fmt.Sprintf("TECH-%02d", i), Kind: "technician_slot", Status: "available", Start: day.AddDate(0, 0, i).Add(9 * time.Hour).Format(time.RFC3339)})
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
		if err := r.Context().Err(); err != nil {
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
		if u.Network == "outage" {
			return result("A regional outage is affecting the North district. Engineers are investigating; no restoration estimate is confirmed. Router restart will not help.", tools.Record{ID: "OUT-001", Kind: "outage", Status: "active", Location: u.Location})
		}
		return result("No active outage in your area.")
	case "network.status", "network.diagnostics":
		summary := "Access line is online."
		if u.Network == "outage" {
			summary = "Regional outage detected. Wait for the network restoration update; do not restart equipment."
		}
		if u.Network == "restricted" {
			summary = "Service is restricted because of an overdue invoice. Check billing."
		}
		if u.Network == "line_fault" {
			summary = "Physical line fault detected. A technician visit is required."
		}
		if u.Router == "unhealthy" {
			summary += " Router is unhealthy; a restart is recommended."
		}
		return result(summary, tools.Record{ID: "line", Kind: "network", Status: u.Network, Description: "Router: " + u.Router})
	case "network.speedtest":
		speed := 92.0
		if u.Network != "online" {
			speed = 0
		}
		if u.Router == "unhealthy" {
			speed = 3
		}
		if u.PlanID == "fiber-500" && u.Network == "online" && u.Router == "healthy" {
			speed = 470
		}
		return result(fmt.Sprintf("Measured download speed: %.0f Mbps.", speed), tools.Record{ID: "speed", Kind: "speedtest", Value: speed, Unit: "Mbps"})
	case "wifi.status", "wifi.diagnostics":
		summary := "Router healthy; Wi-Fi channel " + u.Channel + " has low interference."
		status := "healthy"
		if u.Interference {
			summary = "High interference on channel " + u.Channel + " is causing poor upstairs coverage. Changing to channel 11 is recommended."
			status = "interference"
		}
		if u.Router == "unhealthy" {
			summary = "Router health check failed; restart is recommended."
			status = "unhealthy"
		}
		return result(summary, tools.Record{ID: "RTR-" + u.ID, Kind: "router", Status: status, Description: "Channel " + u.Channel})
	case "wifi.devices":
		return result("Three devices are connected.", tools.Record{ID: "DEV-1", Kind: "device", Name: "Living room TV", Status: "strong signal"}, tools.Record{ID: "DEV-2", Kind: "device", Name: "Upstairs laptop", Status: map[bool]string{true: "weak signal", false: "strong signal"}[u.Interference]}, tools.Record{ID: "DEV-3", Kind: "device", Name: "Mobile phone", Status: "strong signal"})
	case "wifi.restart", "wifi.optimize":
		if u.Network != "online" {
			return tools.Failure("action_blocked", "Equipment changes cannot resolve "+u.Network+". Resolve the access issue first.")
		}
		if name == "wifi.restart" {
			u.Router = "healthy"
			return result("Router restarted successfully. Router health is now normal.")
		}
		ch := a["channel"]
		if ch != "1" && ch != "6" && ch != "11" {
			return tools.Failure("invalid_input", "Channel must be 1, 6 or 11")
		}
		u.Channel = ch
		u.Interference = ch == "6"
		return result("Wi-Fi channel changed to " + ch + ". " + map[bool]string{true: "Interference remains on this channel.", false: "Interference cleared; upstairs signal improved."}[u.Interference])
	case "ticket.list":
		return result("Your support tickets.", owned(b.records["tickets"], u.ID)...)
	case "ticket.details":
		r, ok := find(owned(b.records["tickets"], u.ID), a["ticket_id"])
		if !ok {
			return tools.Failure("not_found", "Ticket not found")
		}
		return result("Ticket details.", r)
	case "ticket.create":
		return b.create("tickets", "ticket", u.ID, a["summary"])
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
			if r.Status == "available" {
				out = append(out, r)
			}
		}
		return result("Available technician visits.", out...)
	case "technician.book":
		for i, r := range b.records["technician_slots"] {
			if r.ID == a["slot_id"] && r.Status == "available" {
				r.Status = "booked"
				r.UserID = u.ID
				b.records["technician_slots"][i] = r
				return result("Technician visit booked.", r)
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
