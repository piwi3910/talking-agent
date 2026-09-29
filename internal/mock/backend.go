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
	for industry, file := range map[string]string{"telecom": "customers", "hospital": "patients"} {
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
	for _, x := range []struct{ industry, name string }{{"telecom", "plans"}, {"hospital", "doctors"}, {"hospital", "departments"}, {"hospital", "facilities"}, {"hospital", "insurance"}} {
		var r []tools.Record
		if err := read(filepath.Join(root, x.industry, x.name+".json"), &r); err != nil {
			return nil, err
		}
		b.catalogs[x.name] = r
	}
	// Relative dates keep sales demos useful without editing fixture dates each week.
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, d := range b.catalogs["doctors"] {
		for offset := 0; offset < 14; offset++ {
			if d.ID == "D002" && offset < 7 {
				continue
			}
			for _, hour := range []int{9, 10, 14} {
				at := day.AddDate(0, 0, offset).Add(time.Duration(hour)*time.Hour + 30*time.Minute)
				if !at.After(now) {
					continue
				}
				b.records["slots"] = append(b.records["slots"], tools.Record{ID: fmt.Sprintf("S-%s-%s-%02d", d.ID, at.Format("20060102"), hour), Kind: "slot", Name: d.Name, RelatedID: d.ID, Specialty: d.Specialty, Start: at.Format(time.RFC3339), Status: "available", Location: d.Location})
			}
		}
	}
	for id := range b.users["hospital"] {
		slot := tools.Record{ID: "A-" + id, Kind: "appointment", Name: "Dr. Ahmed", UserID: id, RelatedID: "D001", Specialty: "Dermatology", Start: day.AddDate(0, 0, 16).Add(time.Duration(len(b.records["appointments"]))*30*time.Minute + 9*time.Hour).Format(time.RFC3339), Status: "booked"}
		b.records["appointments"] = append(b.records["appointments"], slot)
		b.records["referrals"] = append(b.records["referrals"], tools.Record{ID: "REF-" + id, Kind: "referral", UserID: id, Status: "received", Description: "Administrative review complete; ready for scheduling."})
		b.records["prescriptions"] = append(b.records["prescriptions"], tools.Record{ID: "RX-" + id, Kind: "prescription", UserID: id, Status: "ready for collection", Location: "Outpatient pharmacy", Description: "Contact your pharmacist for medication questions."})
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
	for i := 1; i <= 8; i++ {
		b.records["technician_slots"] = append(b.records["technician_slots"], tools.Record{ID: fmt.Sprintf("TECH-%02d", i), Kind: "technician_slot", Status: "available", Start: day.AddDate(0, 0, i).Add(9 * time.Hour).Format(time.RFC3339)})
	}
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
	if req.Industry == "hospital" {
		return b.hospital(u, req.Name, a)
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
func (b *Backend) hospital(u *User, name string, a map[string]string) tools.Result {
	switch name {
	case "patient.lookup", "patient.profile":
		return result("Patient identified.", u.Record)
	case "patient.authenticate":
		return result("Trusted demo identity selected by operator; this is not production authentication.", tools.Record{ID: u.ID, Kind: "identity", Status: "demo_authenticated"})
	case "doctor.search":
		return result("Matching doctors.", filter(b.catalogs["doctors"], a["query"])...)
	case "doctor.details":
		r, ok := find(b.catalogs["doctors"], a["doctor_id"])
		if !ok {
			return tools.Failure("not_found", "Doctor not found")
		}
		return result("Doctor details.", r)
	case "doctor.specialties":
		return result("Available specialties: Dermatology, Cardiology, Orthopedics, Pediatrics, General Medicine.")
	case "department.search":
		return result("Matching departments.", filter(b.catalogs["departments"], a["query"])...)
	case "department.details":
		r, ok := find(b.catalogs["departments"], a["department_id"])
		if !ok {
			return tools.Failure("not_found", "Department not found")
		}
		return result("Department details.", r)
	case "appointment.list":
		return result("Your appointments.", owned(b.records["appointments"], u.ID)...)
	case "appointment.availability":
		for _, key := range []string{"from", "to"} {
			if a[key] != "" {
				if _, err := time.Parse("2006-01-02", a[key]); err != nil {
					return tools.Failure("invalid_input", "Dates must use YYYY-MM-DD")
				}
			}
		}
		if a["from"] != "" && a["to"] != "" && a["from"] > a["to"] {
			return tools.Failure("invalid_input", "Date range is reversed")
		}
		out := []tools.Record{}
		for _, r := range b.records["slots"] {
			if r.Status != "available" || r.Start < time.Now().UTC().Format(time.RFC3339) {
				continue
			}
			if a["doctor_id"] != "" && r.RelatedID != a["doctor_id"] {
				continue
			}
			if a["specialty"] != "" && !strings.EqualFold(a["specialty"], r.Specialty) {
				continue
			}
			date := r.Start[:10]
			if a["from"] != "" && date < a["from"] || a["to"] != "" && date > a["to"] {
				continue
			}
			if a["time_preference"] == "morning" && r.Start[11:13] >= "12" || a["time_preference"] == "afternoon" && r.Start[11:13] < "12" {
				continue
			}
			out = append(out, r)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
		if len(out) > 8 {
			out = out[:8]
		}
		if len(out) == 0 {
			return result("No matching appointments are available for the requested doctor, specialty and dates. Try another doctor or a later date.")
		}
		return result("Available appointments (UTC). Choose a slot ID to book; availability is rechecked at booking.", out...)
	case "appointment.book", "appointment.reschedule":
		old := -1
		if name == "appointment.reschedule" {
			for i, r := range b.records["appointments"] {
				if r.ID == a["appointment_id"] && r.UserID == u.ID && r.Status == "booked" {
					old = i
				}
			}
			if old < 0 {
				return tools.Failure("not_found", "Owned active appointment not found")
			}
		}
		si := -1
		for i, r := range b.records["slots"] {
			if r.ID == a["slot_id"] && r.Status == "available" && r.Start > time.Now().UTC().Format(time.RFC3339) {
				si = i
			}
		}
		if si < 0 {
			return tools.Failure("slot_unavailable", "Appointment slot is no longer available")
		}
		slot := b.records["slots"][si]
		for i, r := range b.records["appointments"] {
			if i != old && r.UserID == u.ID && r.Status == "booked" && r.Start == slot.Start {
				return tools.Failure("appointment_conflict", "You already have an appointment at that time")
			}
		}
		b.records["slots"][si].Status = "booked"
		b.records["slots"][si].UserID = u.ID
		b.serial++
		appointment := slot
		appointment.ID = fmt.Sprintf("A-%04d", b.serial)
		appointment.Kind = "appointment"
		appointment.UserID = u.ID
		appointment.Status = "booked"
		appointment.Description = "Slot " + slot.ID
		if old >= 0 {
			previous := b.records["appointments"][old]
			b.release(previous)
			appointment.ID = previous.ID
			b.records["appointments"][old] = appointment
			return result("Appointment rescheduled.", appointment)
		}
		b.records["appointments"] = append(b.records["appointments"], appointment)
		return result("Appointment booked.", appointment)
	case "appointment.cancel":
		for i, r := range b.records["appointments"] {
			if r.ID == a["appointment_id"] && r.UserID == u.ID && r.Status == "booked" {
				b.release(r)
				b.records["appointments"][i].Status = "cancelled"
				return result("Appointment cancelled.", b.records["appointments"][i])
			}
		}
		return tools.Failure("not_found", "Owned active appointment not found")
	case "insurance.providers":
		return result("Supported insurance providers and plans.", b.catalogs["insurance"]...)
	case "insurance.check":
		for _, r := range b.catalogs["insurance"] {
			if strings.EqualFold(r.Name, a["provider"]) {
				supported := a["plan"] == ""
				for _, plan := range strings.Split(strings.TrimPrefix(r.Description, "Supported plans: "), ", ") {
					if strings.EqualFold(plan, a["plan"]) {
						supported = true
					}
				}
				if !supported {
					return result("This plan is not listed as supported. Contact the insurance desk to verify.")
				}
				return result("Provider is supported for the listed plans. Eligibility is not a guarantee of claim payment.", r)
			}
		}
		return result("Provider not listed as supported. Contact the insurance desk to verify.")
	case "facility.search":
		return result("Matching facilities.", filter(b.catalogs["facilities"], a["query"])...)
	case "facility.directions", "facility.hours":
		r, ok := find(b.catalogs["facilities"], a["facility_id"])
		if !ok {
			return tools.Failure("not_found", "Facility not found")
		}
		return result(r.Name+": "+r.Location+". "+r.Description, r)
	case "referral.status":
		return result("Your referral status.", owned(b.records["referrals"], u.ID)...)
	case "prescription.status":
		return result("Your prescription fulfillment status. Ask your pharmacist about medication use.", owned(b.records["prescriptions"], u.ID)...)
	case "support.create_request":
		return b.create("requests", "request", u.ID, a["summary"])
	}
	return tools.Failure("unknown_tool", "Tool not available in hospital backend")
}
func (b *Backend) release(a tools.Record) {
	for i, s := range b.records["slots"] {
		if s.UserID == a.UserID && s.Start == a.Start && s.RelatedID == a.RelatedID {
			b.records["slots"][i].Status = "available"
			b.records["slots"][i].UserID = ""
		}
	}
}
