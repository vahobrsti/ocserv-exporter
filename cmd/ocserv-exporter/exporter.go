package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/criteo/ocserv-exporter/lib/occtl"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type Exporter struct {
	interval    time.Duration
	listenAddr  string
	occtlCli    *occtl.Client
	promHandler http.Handler

	users []occtl.UsersMessage

	lock sync.Mutex
}

func NewExporter(occtlCli *occtl.Client, listenAddr string, interval time.Duration) *Exporter {
	return &Exporter{
		listenAddr:  listenAddr,
		interval:    interval,
		occtlCli:    occtlCli,
		promHandler: promhttp.Handler(),
	}
}

func (e *Exporter) Run() error {
	// run once to ensure we have data before starting the server
	e.update()

	go func() {
		for range time.Tick(e.interval) {
			e.update()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", e.metricsHandler)

	log.Infof("Listening on http://%s", e.listenAddr)
	return http.ListenAndServe(e.listenAddr, mux)
}

func (e *Exporter) update() {
	e.lock.Lock()
	defer e.lock.Unlock()

	e.updateStatus()
	e.updateUsers()
}

func (e *Exporter) updateStatus() {
	status, err := e.occtlCli.ShowStatus()
	if err != nil {
		log.Errorf("Failed to get server status: %v", err)
		occtlStatusScrapeError.WithLabelValues().Inc()
		vpnActiveSessions.Reset()
		vpnHandledSessions.Reset()
		vpnIPsBanned.Reset()
		return
	}
	vpnStartTime.WithLabelValues().Set(float64(status.RawUpSince))
	vpnActiveSessions.WithLabelValues().Set(float64(status.ActiveSessions))
	vpnHandledSessions.WithLabelValues().Set(float64(status.HandledSessions))
	vpnIPsBanned.WithLabelValues().Set(float64(status.IPsBanned))
	vpnTotalAuthenticationFailures.WithLabelValues().Set(float64(status.TotalAuthenticationFailures))
	vpnSessionsHandled.WithLabelValues().Set(float64(status.SessionsHandled))
	vpnTimedOutSessions.WithLabelValues().Set(float64(status.TimedOutSessions))
	vpnTimedOutIdleSessions.WithLabelValues().Set(float64(status.TimedOutIdleSessions))
	vpnClosedDueToErrorSessions.WithLabelValues().Set(float64(status.ClosedDueToErrorSessions))
	vpnAuthenticationFailures.WithLabelValues().Set(float64(status.AuthenticationFailures))
	vpnAverageAuthTime.WithLabelValues().Set(float64(status.RawAverageAuthTime))
	vpnMaxAuthTime.WithLabelValues().Set(float64(status.RawMaxAuthTime))
	vpnAverageSessionTime.WithLabelValues().Set(float64(status.RawAverageSessionTime))
	vpnMaxSessionTime.WithLabelValues().Set(float64(status.RawMaxSessionTime))
	vpnTX.WithLabelValues().Set(float64(status.RawTX))
	vpnRX.WithLabelValues().Set(float64(status.RawRX))
}

func (e *Exporter) updateUsers() {
	e.users = nil

	vpnUserTX.Reset()
	vpnUserRX.Reset()
	vpnUserStartTime.Reset()
	// Reset new aggregated per-user metrics
	vpnUserAggRX.Reset()
	vpnUserAggTX.Reset()
	vpnUserTotal.Reset()
	vpnUserSessions.Reset()
	users, err := e.occtlCli.ShowUsers()
	if err != nil {
		log.Errorf("Failed to get users details: %v", err)
		occtlUsersScrapeError.WithLabelValues().Inc()
		return
	}
	// user → aggregated stats
	type agg struct {
		RX       int64
		TX       int64
		Sessions int64
	}
	aggMap := make(map[string]*agg)
	for _, u := range users {
		if _, ok := aggMap[u.Username]; !ok {
			aggMap[u.Username] = &agg{}
		}
		aggMap[u.Username].RX += u.RawRX
		aggMap[u.Username].TX += u.RawTX
		aggMap[u.Username].Sessions++
	}
	// Export aggregated metrics. The aggregated need to be modified.
	for username, a := range aggMap {
		total := a.RX + a.TX

		vpnUserAggRX.WithLabelValues(username).Set(float64(a.RX))
		vpnUserAggTX.WithLabelValues(username).Set(float64(a.TX))
		vpnUserTotal.WithLabelValues(username).Set(float64(total))
		vpnUserSessions.WithLabelValues(username).Set(float64(a.Sessions))

		log.Debugf("User %s: RX=%d TX=%d TOTAL=%d SESSIONS=%d",
			username, a.RX, a.TX, total, a.Sessions)
	}

	for _, user := range users {
		vpnUserTX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device).Set(float64(user.RawTX))
		vpnUserRX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device).Set(float64(user.RawRX))
		vpnUserStartTime.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device).Set(float64(user.RawConnectedAt))
	}
}

func (e *Exporter) metricsHandler(rw http.ResponseWriter, r *http.Request) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.promHandler.ServeHTTP(rw, r)
}
