package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
)

const (
	testOmadacID = "0123456789abcdef0123456789abcdef"
	testSiteID   = "0123456789abcdef01234567"
	testSiteName = "Default"
)

const devicesFixture = `{"errorCode":0,"msg":"Success.","result":{"totalRows":2,"currentPage":1,"currentSize":100,"data":[
	{"type":"switch","status":1,"cpuUtil":5,"memUtil":64,"uptime":"3day(s) 1h 46m 13s","mac":"AA-BB-CC-00-00-01","name":"switch","model":"ES208GP v1.0","modelName":"ES208GP","firmwareVersion":"1.0.4 Build 20260329 Rel.14489","ip":"192.168.1.5","sn":"REDACTED","subtype":"es","deviceSeriesType":0},
	{"type":"ap","status":1,"cpuUtil":1,"memUtil":34,"uptime":"3day(s) 0h 12m 03s","mac":"AA-BB-CC-00-00-02","name":"ap-bedroom","model":"EAP610(EU) v4.0","firmwareVersion":"1.8.0 Build 20260312 Rel. 14111","ip":"192.168.1.8"}
]}}`

const poeFixture = `{"errorCode":0,"msg":"Success.","result":[
	{"mac":"AA-BB-CC-00-00-01","name":"switch","portNum":8,"totalPower":64,"totalPowerUsed":14,"totalPercentUsed":22.19,"poePorts":[
		{"portId":1,"poeSupported":true,"poeEnabled":false},
		{"portId":3,"poeSupported":true,"poeEnabled":true,"poePower":6,"poePercent":20}
	]}
]}`

const clientsFixture = `{"errorCode":0,"msg":"Success.","result":{"totalRows":1,"currentPage":1,"currentSize":100,"data":[
	{"mac":"11-22-33-44-55-66","name":"example-phone","vendor":"Unknown","ssid":"MySSID","apMac":"AA-BB-CC-00-00-02","apName":"ap-bedroom","channel":40,"rssi":-44,"snr":51,"signalLevel":100,"rxRate":24000,"txRate":1080000,"trafficDown":284631751,"trafficUp":4805760,"downPacket":195919,"upPacket":31906,"uptime":994,"deviceCategory":"Mobile","wifiMode":6,"vid":12,"active":true}
]}}`

// fixtureServer is a stand-in Omada controller used by tests. It serves the
// sample payloads from the design doc and can optionally simulate a token
// expiring mid-scrape (errorCode -44112) on the first /devices request.
type fixtureServer struct {
	expireOnce     bool
	tokenMints     int32
	deviceRequests int32
}

func newFixtureServer(expireOnce bool) (*httptest.Server, *fixtureServer) {
	fs := &fixtureServer{expireOnce: expireOnce}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"result":{"controllerVer":"6.2.10.17","omadacId":%q}}`, testOmadacID)
	})

	mux.HandleFunc("/openapi/authorize/token", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&fs.tokenMints, 1)
		fmt.Fprintf(w, `{"errorCode":0,"msg":"Open API Get Access Token successfully.","result":{"accessToken":"token-%d","tokenType":"bearer","expiresIn":7200}}`, n)
	})

	mux.HandleFunc(fmt.Sprintf("/openapi/v1/%s/sites", testOmadacID), func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"errorCode":0,"msg":"Success.","result":{"totalRows":1,"currentPage":1,"currentSize":100,"data":[{"siteId":%q,"name":%q}]}}`, testSiteID, testSiteName)
	})

	mux.HandleFunc(fmt.Sprintf("/openapi/v1/%s/sites/%s/devices", testOmadacID, testSiteID), func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&fs.deviceRequests, 1)
		if fs.expireOnce && n == 1 {
			fmt.Fprint(w, `{"errorCode":-44112,"msg":"The access token has expired, please refresh."}`)
			return
		}
		fmt.Fprint(w, devicesFixture)
	})

	mux.HandleFunc(fmt.Sprintf("/openapi/v1/%s/sites/%s/dashboard/poe-usage", testOmadacID, testSiteID), func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, poeFixture)
	})

	mux.HandleFunc(fmt.Sprintf("/openapi/v1/%s/sites/%s/clients", testOmadacID, testSiteID), func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, clientsFixture)
	})

	srv := httptest.NewServer(mux)
	return srv, fs
}
