package mock

import (
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"time"

	"enterprise-ai-demo/internal/tools"
)

// pick returns a stable pseudo-random value in [0,1) for the user and a topic,
// so one customer always sees the same line, while different customers differ.
// The salt changes the value for the same user when a test or operator wants a
// different draw.
func pick(user, topic string, salt int) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(user + "|" + topic + "|" + strconv.Itoa(salt)))
	return float64(h.Sum32()%10000) / 10000
}

// between maps a unit value into [lo,hi].
func between(unit, lo, hi float64) float64 { return lo + unit*(hi-lo) }

func metric(id, name string, value float64, unit, status string) tools.Record {
	return tools.Record{ID: id, Kind: "metric", Name: name, Value: value, Unit: unit, Status: status}
}

// planMbps is the headline speed of the customer's broadband plan.
func planMbps(planID string) float64 {
	if n, err := strconv.Atoi(strings.TrimPrefix(planID, "fiber-")); err == nil && n > 0 {
		return float64(n)
	}
	return 100
}

// line is one measured snapshot of a customer's access line.
type line struct {
	sync, up     float64 // synchronised rate, Mbps
	power        float64 // optical receive power, dBm
	loss         float64 // packet loss, percent
	latency, jit float64 // milliseconds
	fault        string  // "", "router", "line", "minor"
}

func (b *Backend) lineFor(u *User) line {
	plan := planMbps(u.PlanID)
	l := line{
		sync:    plan,
		up:      plan * between(pick(u.ID, "up", 0), 0.9, 1),
		power:   -between(pick(u.ID, "power", 0), 16.5, 21.5),
		loss:    between(pick(u.ID, "loss", 0), 0, 0.3),
		latency: between(pick(u.ID, "lat", 0), 6, 13),
		jit:     between(pick(u.ID, "jit", 0), 1, 3.5),
	}
	switch {
	case u.Network == "line_fault":
		l.fault = "line"
		l.sync = plan * between(pick(u.ID, "sync", 0), 0.2, 0.45)
		l.up = l.sync * 0.8
		l.power = -between(pick(u.ID, "power", 1), 27, 30)
		l.loss = between(pick(u.ID, "loss", 1), 12, 22)
		l.latency = between(pick(u.ID, "lat", 1), 70, 140)
		l.jit = between(pick(u.ID, "jit", 1), 25, 60)
	case u.Router == "unhealthy":
		l.fault = "router"
		l.loss = between(pick(u.ID, "loss", 2), 5, 9)
		l.latency = between(pick(u.ID, "lat", 2), 40, 90)
		l.jit = between(pick(u.ID, "jit", 2), 15, 35)
	case u.Network == "online" && pick(u.ID, "minor", 0) < 0.25:
		// Healthy line with a little Wi-Fi-side loss, worth mentioning but not fixing.
		l.fault = "minor"
		l.loss = between(pick(u.ID, "loss", 3), 0.7, 1.2)
	}
	return l
}

func (l line) metrics() []tools.Record {
	loss := "good"
	switch {
	case l.loss >= 3:
		loss = "poor"
	case l.loss >= 0.5:
		loss = "fair"
	}
	signal := "good"
	if l.power < -25 {
		signal = "weak"
	}
	return []tools.Record{
		metric("sync-down", "Line sync download", round(l.sync), "Mbps", ""),
		metric("sync-up", "Line sync upload", round(l.up), "Mbps", ""),
		metric("optical-power", "Optical signal", round1(l.power), "dBm", signal),
		metric("packet-loss", "Packet loss", round2(l.loss), "%", loss),
		metric("latency", "Latency", round(l.latency), "ms", ""),
		metric("jitter", "Jitter", round(l.jit), "ms", ""),
	}
}

func round(v float64) float64  { return math.Round(v) }
func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// telecomNetwork answers network.status (brief) and network.diagnostics (full).
func (b *Backend) telecomNetwork(u *User, full bool) tools.Result {
	summary := "Access line is online."
	guidance := ""
	l := b.lineFor(u)
	rs := []tools.Record{{ID: "line", Kind: "network", Status: u.Network, Description: "Router: " + u.Router}}
	switch u.Network {
	case "outage":
		summary = "Regional outage detected. Wait for the network restoration update; do not restart equipment. The line shows no signal from the exchange, so the fault is on the network side."
		guidance = "Give the outage details from network.outages if asked; do not offer a restart."
	case "restricted":
		summary = "Service is restricted because of an overdue invoice. Check billing. The line itself is in sync and healthy."
		guidance = "Do not suggest equipment changes; explain the balance and how to pay."
	case "line_fault":
		summary = fmt.Sprintf("Physical line fault detected. A technician visit is required. The optical signal is weak at %.1f dBm, the line only syncs at %.0f of %.0f Mbps and packet loss is %.0f percent.", l.power, l.sync, planMbps(u.PlanID), l.loss)
		guidance = "High loss and a weak signal are a physical fault, so a restart will not help. Offer to book a technician with technician.availability."
	default:
		summary += fmt.Sprintf(" It is synchronised at %.0f megabits down and %.0f up, with %.1f percent packet loss and %.0f milliseconds latency.", l.sync, l.up, l.loss, l.latency)
		if full {
			summary += fmt.Sprintf(" Optical signal is a healthy %.1f dBm.", l.power)
		}
		switch l.fault {
		case "router":
			guidance = "Packet loss this high on a healthy line points at the router; offer a restart with wifi.restart (needs confirmation)."
		case "minor":
			guidance = "Loss is slightly elevated but the line is fine; if they report slow Wi-Fi, check wifi.diagnostics for channel congestion."
		default:
			guidance = "The line is healthy. If they still have slow or patchy Wi-Fi, check wifi.diagnostics next."
		}
	}
	if u.Router == "unhealthy" && u.Network == "online" {
		summary += " Router is unhealthy; a restart is recommended."
	}
	if full {
		rs = append(rs, l.metrics()...)
	}
	return result(summary+tools.GuidanceMarker+guidance, rs...)
}

func (b *Backend) telecomSpeedtest(u *User) tools.Result {
	l := b.lineFor(u)
	plan := planMbps(u.PlanID)
	down, up, ping, jitter := l.sync*between(pick(u.ID, "speed", 0), 0.93, 0.99), l.up*between(pick(u.ID, "speed", 1), 0.9, 0.98), l.latency, l.jit
	// A fresh draw for each test: a second run is close to the first but not identical.
	down *= between(float64(time.Now().UnixNano()%1000)/1000, 0.97, 1.02)
	guidance := ""
	switch {
	case u.Network != "online":
		down, up, ping, jitter = 0, 0, 0, 0
		guidance = "The test could not reach the network; explain the reason from the diagnostics instead of quoting speeds."
	case u.Router == "unhealthy":
		down, up, ping, jitter = 3, 1, l.latency, l.jit
		guidance = fmt.Sprintf("That is far below the %.0f Mbps plan. The line is fine, so recommend a router restart with wifi.restart.", plan)
	case down >= plan*0.85:
		guidance = fmt.Sprintf("That is in line with the %.0f Mbps plan: no speed problem on the line. If a device is slow, look at Wi-Fi.", plan)
	default:
		guidance = fmt.Sprintf("Below the %.0f Mbps plan; suggest wifi.diagnostics to see whether Wi-Fi is the limit.", plan)
	}
	summary := fmt.Sprintf("Measured download speed: %.0f Mbps.", down)
	if u.Network == "online" {
		summary += fmt.Sprintf(" Upload %.0f Mbps, ping %.0f ms, jitter %.0f ms.", up, ping, jitter)
	}
	return result(summary+tools.GuidanceMarker+guidance,
		tools.Record{ID: "speed", Kind: "speedtest", Value: down, Unit: "Mbps"},
		metric("speed-up", "Upload", round(up), "Mbps", ""),
		metric("speed-ping", "Ping", round(ping), "ms", ""),
		metric("speed-jitter", "Jitter", round(jitter), "ms", ""))
}

// telecomOutage describes an active outage with an estimate and a reference.
func (b *Backend) telecomOutage(u *User) tools.Result {
	if u.Network != "outage" {
		return result("No active outage in your area. The local cabinet and exchange both report normal service." + tools.GuidanceMarker + "Rule out an outage and continue with the line or Wi-Fi checks.")
	}
	eta := time.Now().In(telecomZone).Add(time.Duration(150+int(pick(u.ID, "eta", 0)*90)) * time.Minute).Truncate(15 * time.Minute)
	ticket := fmt.Sprintf("INC-%05d", 40000+int(pick(u.ID, "inc", 0)*9000))
	text := fmt.Sprintf("Engineers are on site and service is expected back around %s, fault reference %s.", clockWords(eta), ticket)
	return result("A regional outage is affecting the North district. "+text+" A restart will not help."+tools.GuidanceMarker+"Offer a text message when service returns; do not suggest equipment changes.",
		tools.Record{ID: "OUT-001", Kind: "outage", Status: "active", Location: u.Location, Description: text, Start: ""})
}

// telecomWifi answers wifi.status and wifi.diagnostics.
func (b *Backend) telecomWifi(u *User) tools.Result {
	util := int(between(pick(u.ID, "util", 0), 12, 30))
	neighbours := int(between(pick(u.ID, "nb", 0), 3, 8))
	laptop := -between(pick(u.ID, "rssi", 0), 52, 60)
	summary := fmt.Sprintf("Router healthy; Wi-Fi channel %s has low interference (%d percent busy, %d neighbouring networks), and the upstairs laptop signal is a strong %.0f dBm.", u.Channel, util, neighbours, laptop)
	status, guidance := "healthy", "Wi-Fi looks fine; if a single device is slow, check that device."
	if u.Interference {
		util = int(between(pick(u.ID, "util", 1), 70, 88))
		neighbours = int(between(pick(u.ID, "nb", 1), 11, 18))
		laptop = -between(pick(u.ID, "rssi", 1), 74, 80)
		summary = fmt.Sprintf("High interference on channel %s is causing poor upstairs coverage: the channel is %d percent busy with %d neighbouring networks, and the upstairs laptop signal is a weak %.0f dBm. Changing to channel 11 is recommended.", u.Channel, util, neighbours, laptop)
		status = "interference"
		guidance = "Offer to switch to channel 11 with wifi.optimize (needs confirmation); upstairs signal should improve noticeably."
	}
	if u.Router == "unhealthy" {
		summary = "Router health check failed; restart is recommended. The router is dropping packets and its memory is nearly full."
		status = "unhealthy"
		guidance = "Offer a restart with wifi.restart (needs confirmation)."
	}
	return result(summary+tools.GuidanceMarker+guidance, tools.Record{ID: "RTR-" + u.ID, Kind: "router", Status: status, Description: "Channel " + u.Channel},
		metric("wifi-busy", "Channel busy", float64(util), "%", ""), metric("wifi-neighbours", "Neighbouring networks", float64(neighbours), "", ""), metric("wifi-laptop", "Upstairs laptop signal", round(laptop), "dBm", ""))
}
