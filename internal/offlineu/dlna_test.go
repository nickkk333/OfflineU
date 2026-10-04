package offlineu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRenderer is a UPnP renderer reduced to what OfflineU talks to: a device
// description plus one SOAP endpoint that records every action it received.
type fakeRenderer struct {
	server  *httptest.Server
	actions []string
	bodies  []string
	mutex   sync.Mutex
	fault   bool
}

const fakeDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>Living Room TV</friendlyName>
    <manufacturer>ACME</manufacturer>
    <modelName>TV-9000</modelName>
    <UDN>uuid:fake-renderer</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
        <controlURL>/rc/control</controlURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <controlURL>/avt/control</controlURL>
      </service>
    </serviceList>
    <deviceList>
      <device>
        <friendlyName>Unrelated</friendlyName>
        <serviceList>
          <service>
            <serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
            <controlURL>/cm/control</controlURL>
          </service>
        </serviceList>
      </device>
    </deviceList>
  </device>
</root>`

func newFakeRenderer(t *testing.T) *fakeRenderer {
	t.Helper()
	fake := &fakeRenderer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = io.WriteString(w, fakeDescription)
	})
	mux.HandleFunc("/avt/control", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		action := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
		if index := strings.Index(action, "#"); index >= 0 {
			action = action[index+1:]
		}
		fake.mutex.Lock()
		fake.actions = append(fake.actions, action)
		fake.bodies = append(fake.bodies, string(body))
		broken := fake.fault
		fake.mutex.Unlock()

		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		if broken {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `<s:Envelope><s:Body><s:Fault><detail><UPnPError>`+
				`<errorCode>714</errorCode><errorDescription>Illegal mime-type</errorDescription>`+
				`</UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		_, _ = io.WriteString(w, `<s:Envelope><s:Body><u:`+action+`Response `+
			`xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"></u:`+action+`Response>`+
			`</s:Body></s:Envelope>`)
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

// renderer returns the Renderer OfflineU builds from the fake description.
func (f *fakeRenderer) renderer(t *testing.T) Renderer {
	t.Helper()
	device, err := fetchRenderer(&http.Client{Timeout: 5 * time.Second}, f.server.URL+"/desc.xml")
	if err != nil {
		t.Fatalf("cannot read the fake description: %v", err)
	}
	return *device
}

func (f *fakeRenderer) recorded() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string{}, f.actions...)
}

func (f *fakeRenderer) bodyOf(index int) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if index >= len(f.bodies) {
		return ""
	}
	return f.bodies[index]
}

func (f *fakeRenderer) setFault(broken bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.fault = broken
}

// seedRenderers puts a renderer into the hub's cache, so the HTTP layer
// resolves it without running a real SSDP search inside the test.
func seedRenderers(hub *DLNAHub, devices ...Renderer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.renderers = devices
	hub.fetchedAt = time.Now()
}

func TestFakeRendererDescriptionYieldsAnAVTransportRenderer(t *testing.T) {
	fake := newFakeRenderer(t)
	device := fake.renderer(t)

	if device.Name != "Living Room TV" {
		t.Errorf("name = %q", device.Name)
	}
	if device.UDN != "uuid:fake-renderer" {
		t.Errorf("udn = %q", device.UDN)
	}
	if device.Manufacturer != "ACME" || device.Model != "TV-9000" {
		t.Errorf("manufacturer/model = %q/%q", device.Manufacturer, device.Model)
	}
	if device.ControlURL != fake.server.URL+"/avt/control" {
		t.Errorf("control url = %q", device.ControlURL)
	}
	if device.ServiceType != avTransportService {
		t.Errorf("service type = %q", device.ServiceType)
	}
	if device.Address != "127.0.0.1" {
		t.Errorf("address = %q", device.Address)
	}
}

func TestCastingSendsSetURIAndPlay(t *testing.T) {
	fake := newFakeRenderer(t)
	hub := NewDLNAHub()

	err := hub.Cast(fake.renderer(t), MediaItem{
		Title: "Intro",
		URL:   "http://192.168.1.5:5000/files/Section%201/01%20-%20Intro.mp4",
		Mime:  "video/mp4",
		Class: "object.item.videoItem",
	}, 0)
	if err != nil {
		t.Fatalf("cast failed: %v", err)
	}

	actions := fake.recorded()
	if len(actions) != 2 || actions[0] != "SetAVTransportURI" || actions[1] != "Play" {
		t.Fatalf("actions = %v", actions)
	}
	body := fake.bodyOf(0)
	if !strings.Contains(body, "http://192.168.1.5:5000/files/Section%201/01%20-%20Intro.mp4") {
		t.Errorf("the media URL is missing from the request: %s", body)
	}
	// The metadata is XML, so it arrives escaped inside the SOAP argument.
	if !strings.Contains(body, "object.item.videoItem") || !strings.Contains(body, "&lt;dc:title&gt;Intro&lt;/dc:title&gt;") {
		t.Errorf("the DIDL metadata is missing: %s", body)
	}
	if !strings.Contains(body, `SOAPACTION`) && !strings.Contains(fake.bodyOf(1), "Speed") {
		t.Errorf("the Play request is incomplete: %s", fake.bodyOf(1))
	}
}

func TestCastingSeeksToTheResumePosition(t *testing.T) {
	fake := newFakeRenderer(t)
	hub := NewDLNAHub()

	if err := hub.Cast(fake.renderer(t), MediaItem{Title: "Intro", URL: "http://10.0.0.2/x.mp4", Mime: "video/mp4"}, 90); err != nil {
		t.Fatalf("cast failed: %v", err)
	}
	actions := fake.recorded()
	if len(actions) != 3 || actions[2] != "Seek" {
		t.Fatalf("actions = %v", actions)
	}
	if body := fake.bodyOf(2); !strings.Contains(body, "<Target>00:01:30</Target>") {
		t.Errorf("seek target missing: %s", body)
	}
}

func TestCastingReportsAUPnPFault(t *testing.T) {
	fake := newFakeRenderer(t)
	fake.setFault(true)
	hub := NewDLNAHub()

	err := hub.Cast(fake.renderer(t), MediaItem{Title: "Intro", URL: "http://10.0.0.2/x.mp4"}, 0)
	if err == nil {
		t.Fatal("expected the fault to be reported")
	}
	if !strings.Contains(err.Error(), "Illegal mime-type") {
		t.Errorf("error = %v", err)
	}
	if actions := fake.recorded(); len(actions) != 1 {
		t.Errorf("a failed SetAVTransportURI must not be followed by Play: %v", actions)
	}
}

func TestControlSendsOnlyKnownActions(t *testing.T) {
	fake := newFakeRenderer(t)
	hub := NewDLNAHub()
	device := fake.renderer(t)

	if err := hub.Control(device, "stop"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if err := hub.Control(device, "pause"); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if err := hub.Control(device, "rewind"); err == nil {
		t.Error("an unknown action was accepted")
	}
	if actions := fake.recorded(); len(actions) != 2 || actions[0] != "Stop" || actions[1] != "Pause" {
		t.Errorf("actions = %v", actions)
	}
}

func TestDIDLMetadataEscapesAndClassifiesMedia(t *testing.T) {
	meta := didlMetadata(MediaItem{
		Title: `Tom & Jerry <live>`,
		URL:   "http://10.0.0.2/a.mp3",
		Mime:  "audio/mpeg",
		Class: "object.item.audioItem.musicTrack",
	})
	if strings.Contains(meta, "<live>") || !strings.Contains(meta, "&lt;live&gt;") {
		t.Errorf("the title was not escaped: %s", meta)
	}
	if !strings.Contains(meta, `Tom &amp; Jerry`) {
		t.Errorf("the ampersand was not escaped: %s", meta)
	}
	if !strings.Contains(meta, "object.item.audioItem.musicTrack") {
		t.Errorf("class missing: %s", meta)
	}
	if !strings.Contains(meta, "http-get:*:audio/mpeg:*") {
		t.Errorf("protocolInfo missing: %s", meta)
	}
}

func TestSoapEnvelopeAndDurationFormat(t *testing.T) {
	envelope := soapEnvelope(avTransportService, "SetAVTransportURI", [][2]string{{"CurrentURI", "http://10.0.0.2/a b.mp4"}})
	if !strings.Contains(envelope, `xmlns:u="`+avTransportService+`"`) {
		t.Errorf("service namespace missing: %s", envelope)
	}
	if !strings.Contains(envelope, "<CurrentURI>http://10.0.0.2/a b.mp4</CurrentURI>") {
		t.Errorf("argument missing: %s", envelope)
	}
	if got := formatUPnPDuration(3725); got != "01:02:05" {
		t.Errorf("duration = %q", got)
	}
	if got := formatUPnPDuration(-5); got != "00:00:00" {
		t.Errorf("negative duration = %q", got)
	}
}

func TestSoapFaultMessageExtraction(t *testing.T) {
	if message := soapFaultMessage(`<UPnPError><errorCode>701</errorCode><errorDescription>Transition not available</errorDescription></UPnPError>`); message != "Transition not available (UPnP 701)" {
		t.Errorf("message = %q", message)
	}
	if message := soapFaultMessage(`<u:SetAVTransportURIResponse></u:SetAVTransportURIResponse>`); message != "" {
		t.Errorf("a successful answer was read as a fault: %q", message)
	}
}

func TestDLNADevicesEndpointListsTheCachedRenderers(t *testing.T) {
	env := newTestEnv(t)
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	payload := struct {
		Enabled bool       `json:"enabled"`
		Devices []Renderer `json:"devices"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/dlna/devices", nil), &payload)
	if !payload.Enabled {
		t.Error("expected dlna to be enabled by default")
	}
	if len(payload.Devices) != 1 || payload.Devices[0].Name != "Living Room TV" {
		t.Fatalf("devices = %+v", payload.Devices)
	}
}

func TestDLNAEndpointsRefuseWhenDisabled(t *testing.T) {
	env := newTestEnv(t)
	t.Setenv(EnvDLNA, "off")
	env.cfg = ConfigFromEnv()
	env.app = NewApp(env.cfg)

	for _, target := range []string{"/api/dlna/devices", "/api/dlna/cast", "/api/dlna/control"} {
		method := http.MethodGet
		body := any(nil)
		if target != "/api/dlna/devices" {
			method = http.MethodPost
			body = map[string]string{"device": "uuid:fake-renderer"}
		}
		if response := env.request(method, target, body); response.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", target, response.Code)
		}
	}
}

func TestDLNACastEndpointPushesTheLessonToTheRenderer(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":        "uuid:fake-renderer",
		"lesson_path":   "Section 1/01 - Intro.mp4",
		"start_seconds": 30,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	payload := struct {
		Device string `json:"device"`
		URL    string `json:"url"`
		Title  string `json:"title"`
	}{}
	env.decode(response, &payload)
	if payload.Device != "Living Room TV" || payload.Title != "Intro" {
		t.Errorf("payload = %+v", payload)
	}
	// The renderer fetches the file itself, so it needs an absolute URL built
	// from the host the browser used.
	if payload.URL != "http://example.com/files/Section%201/01%20-%20Intro.mp4" {
		t.Errorf("url = %q", payload.URL)
	}
	if body := fake.bodyOf(0); !strings.Contains(body, payload.URL) {
		t.Errorf("the renderer did not receive the URL: %s", body)
	}
}

func TestDLNACastEndpointRejectsLessonsWithoutMedia(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/02 - Notes.txt",
	})
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.Code)
	}
	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:not-on-the-network",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusNotFound {
		t.Errorf("unknown device: status = %d, want 404", response.Code)
	}
}

func TestDLNAControlEndpointForwardsTheAction(t *testing.T) {
	env := newTestEnv(t)
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "Stop",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if actions := fake.recorded(); len(actions) != 1 || actions[0] != "Stop" {
		t.Errorf("actions = %v", actions)
	}
	if response := env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "explode",
	}); response.Code != http.StatusBadRequest {
		t.Errorf("bad action: status = %d, want 400", response.Code)
	}
}
