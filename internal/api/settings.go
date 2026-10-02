package api

import (
	"enterprise-ai-demo/internal/telephony"
	"errors"
	"net/http"
)

func (a *API) settingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/settings/sip", func(w http.ResponseWriter, r *http.Request) {
		if a.PhoneGateway == nil {
			fail(w, 503, "Gateway unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, a.PhoneGateway.Snapshot())
	})
	m.HandleFunc("POST /api/settings/sip", func(w http.ResponseWriter, r *http.Request) {
		if a.PhoneGateway == nil {
			fail(w, 503, "Gateway unavailable")
			return
		}
		var in telephony.GatewayConfig
		if decode(w, r, &in) != nil {
			fail(w, 400, "Invalid gateway settings")
			return
		}
		if err := a.PhoneGateway.Save(in); err != nil {
			fail(w, 400, err.Error())
			return
		}
		write(w, 200, a.PhoneGateway.Snapshot())
	})
	for _, action := range []string{"connect", "disconnect"} {
		m.HandleFunc("POST /api/settings/sip/"+action, func(w http.ResponseWriter, r *http.Request) {
			if a.PhoneGateway == nil {
				fail(w, 503, "Gateway unavailable")
				return
			}
			var err error
			if action == "connect" {
				err = a.PhoneGateway.Connect()
			} else {
				err = a.PhoneGateway.Disconnect()
			}
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			write(w, 200, a.PhoneGateway.Snapshot())
		})
	}

	m.HandleFunc("GET /api/settings/phone", func(w http.ResponseWriter, r *http.Request) {
		if a.PhoneSettings == nil {
			fail(w, 503, "Phone settings unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, a.PhoneSettings.Snapshot())
	})
	m.HandleFunc("POST /api/settings/phone", func(w http.ResponseWriter, r *http.Request) {
		if a.PhoneSettings == nil {
			fail(w, 503, "Phone settings unavailable")
			return
		}
		var in telephony.PhoneSettings
		if err := decode(w, r, &in); err != nil {
			fail(w, 400, "Invalid phone settings")
			return
		}
		for id, p := range in.Personas {
			c := a.Agents[id]
			if c == nil {
				fail(w, 400, "Unknown persona")
				return
			}
			if p.UserID != "" {
				users, err := a.users(r.Context(), c)
				if err != nil {
					fail(w, 502, "Account service unavailable")
					return
				}
				valid := false
				for _, u := range users {
					if u.ID == p.UserID {
						valid = true
					}
				}
				if !valid {
					fail(w, 400, "Phone account does not belong to selected persona")
					return
				}
			}
		}
		saved, err := a.PhoneSettings.Save(in)
		if errors.Is(err, telephony.ErrSettingsConflict) {
			fail(w, 409, err.Error())
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, saved)
	})
}
