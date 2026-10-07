// Command fakerenderer is a fake DLNA MediaRenderer used by the OfflineU E2E
// tests. It answers real SSDP M-SEARCH datagrams, serves a real device
// description and accepts the real SOAP AVTransport actions the server sends,
// so a full cast (discovery -> SetAVTransportURI/Play -> watchdog polling ->
// loop/next/once handling) can be exercised without a TV on the network.
//
// The position it reports advances in real time from the moment Play is
// received, matching the 6 second MP4s of courses/E2E Course: the watchdog
// sees the lesson run to its end exactly like it would on a real device.
//
// Endpoints:
//
//	GET  /desc.xml     device description (MediaRenderer + AVTransport)
//	POST /avt/control  SOAP endpoint (SetAVTransportURI, Play, Pause, Stop,
//	                   Seek, GetPositionInfo, GetTransportInfo)
//	GET  /state        JSON: transport, current URI, full action log
//	POST /reset        clear the action log
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	avTransportService = "urn:schemas-upnp-org:service:AVTransport:1"
	rendererDeviceType = "urn:schemas-upnp-org:device:MediaRenderer:1"
	ssdpMulticast      = "239.255.255.250:1900"
)

// event is one SOAP/SSDP interaction, kept for assertions.
type event struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	URI    string    `json:"uri,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// device is the renderer state machine.
type device struct {
	mu       sync.Mutex
	udn      string
	name     string
	duration time.Duration
	state    string // STOPPED, PLAYING, PAUSED_PLAYBACK
	uri      string
	playAt   time.Time
	offset   time.Duration
	events   []event
}

func (d *device) record(action, uri, detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, event{At: time.Time{}, Action: action, URI: uri, Detail: detail})
	d.events[len(d.events)-1].At = time.Now()
}

// positionLocked is where the device is right now; callers hold d.mu.
// A playing renderer stops once the file runs out, just like a real one.
func (d *device) positionLocked() time.Duration {
	position := d.offset
	if d.state == "PLAYING" {
		position += time.Since(d.playAt)
	}
	if position > d.duration {
		position = d.duration
		if d.state == "PLAYING" {
			d.state = "STOPPED"
		}
	}
	if position < 0 {
		position = 0
	}
	return position
}

func (d *device) positionSecondsLocked() float64 {
	return d.positionLocked().Seconds()
}

func (d *device) transportState() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// clock renders seconds the way UPnP spells them: H:MM:SS.fff.
func clock(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%d:%02d:%02d.%03d", total/3600, (total/60)%60, total%60,
		int((seconds-float64(total))*1000))
}

func main() {
	httpAddr := flag.String("http", "127.0.0.1:7920", "address of the description/SOAP HTTP server")
	advertise := flag.String("advertise", "", "IP put into the SSDP LOCATION header (default: -http host)")
	udn := flag.String("udn", "uuid:offlineu-e2e-renderer", "device UDN")
	name := flag.String("name", "OfflineU E2E Fake Renderer", "friendly name")
	duration := flag.Duration("duration", 6*time.Second, "media length reported to the server")
	ssdp := flag.Bool("ssdp", true, "answer SSDP M-SEARCH on 239.255.255.250:1900")
	flag.Parse()

	locationIP := *advertise
	if locationIP == "" {
		host, _, err := net.SplitHostPort(*httpAddr)
		if err == nil {
			locationIP = host
		}
		if locationIP == "" || locationIP == "0.0.0.0" {
			locationIP = "127.0.0.1"
		}
	}
	httpPort := 0
	if _, port, err := net.SplitHostPort(*httpAddr); err == nil {
		fmt.Sscanf(port, "%d", &httpPort)
	}
	location := fmt.Sprintf("http://%s:%d/desc.xml", locationIP, httpPort)

	dev := &device{udn: *udn, name: *name, duration: *duration, state: "STOPPED"}

	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <URLBase>http://%s:%d/</URLBase>
  <device>
    <deviceType>%s</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>OfflineU E2E</manufacturer>
    <modelName>FakeRenderer</modelName>
    <UDN>%s</UDN>
    <serviceList>
      <service>
        <serviceType>%s</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <controlURL>/avt/control</controlURL>
        <eventSubURL>/avt/event</eventSubURL>
        <SCPDURL>/avt/scpd.xml</SCPDURL>
      </service>
    </serviceList>
  </device>
</root>`, locationIP, httpPort, rendererDeviceType, dev.name, dev.udn, avTransportService)
	})
	mux.HandleFunc("/avt/scpd.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		io.WriteString(w, `<?xml version="1.0"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion></scpd>`)
	})
	mux.HandleFunc("/avt/control", dev.handleControl)
	mux.HandleFunc("/avt/event", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		dev.mu.Lock()
		snapshot := struct {
			UDN       string  `json:"udn"`
			Name      string  `json:"name"`
			Transport string  `json:"transport"`
			URI       string  `json:"uri"`
			Position  string  `json:"position"`
			Duration  string  `json:"duration"`
			Events    []event `json:"events"`
		}{
			UDN:       dev.udn,
			Name:      dev.name,
			Transport: dev.state,
			URI:       dev.uri,
			Position:  clock(dev.positionSecondsLocked()),
			Duration:  clock(dev.duration.Seconds()),
			Events:    append([]event{}, dev.events...),
		}
		dev.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		dev.mu.Lock()
		dev.events = nil
		dev.uri = ""
		dev.offset = 0
		dev.state = "STOPPED"
		dev.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	if *ssdp {
		if err := startSSDP(dev, location); err != nil {
			log.Printf("ssdp: disabled (%v)", err)
		}
	}
	log.Printf("fake renderer %s at %s (LOCATION %s)", dev.udn, *httpAddr, location)
	log.Fatal(http.ListenAndServe(*httpAddr, mux))
}

// startSSDP joins the multicast group on every interface that supports
// multicast and answers every M-SEARCH with the LOCATION of this renderer.
//
// One socket per interface: Windows delivers a looped-back M-SEARCH only to
// group members on the interface the sender used, and picking "the" interface
// (nil) is a guess that easily misses the one the server routes through.
//
// Every socket gets enableMulticastLoopback applied right away, because
// net.ListenMulticastUDP clears IP_MULTICAST_LOOP - see the note there.
func startSSDP(d *device, location string) error {
	group, err := net.ResolveUDPAddr("udp4", ssdpMulticast)
	if err != nil {
		return err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	opened := 0
	for _, ifi := range interfaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		connection, err := net.ListenMulticastUDP("udp4", &ifi, group)
		if err != nil {
			log.Printf("ssdp: cannot listen on %s: %v", ifi.Name, err)
			continue
		}
		if err := enableMulticastLoopback(connection); err != nil {
			log.Printf("ssdp: cannot re-enable multicast loopback on %s: %v", ifi.Name, err)
		}
		opened++
		log.Printf("ssdp: listening on %s (%s)", ssdpMulticast, ifi.Name)
		go answerMSearch(connection, d, location)
	}
	if opened == 0 {
		connection, err := net.ListenMulticastUDP("udp4", nil, group)
		if err != nil {
			return err
		}
		if err := enableMulticastLoopback(connection); err != nil {
			log.Printf("ssdp: cannot re-enable multicast loopback on the default interface: %v", err)
		}
		opened++
		log.Printf("ssdp: listening on %s (default interface)", ssdpMulticast)
		go answerMSearch(connection, d, location)
	}
	return nil
}

// answerMSearch reads one SSDP socket until it closes.
func answerMSearch(connection *net.UDPConn, d *device, location string) {
	buffer := make([]byte, 65535)
	for {
		count, source, err := connection.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("ssdp: read: %v", err)
			return
		}
		message := string(buffer[:count])
		if !strings.HasPrefix(message, "M-SEARCH") {
			continue
		}
		if !strings.Contains(strings.ToUpper(message), "MAN:") {
			continue
		}
		st := headerValue(message, "ST")
		if st == "" {
			st = rendererDeviceType
		}
		reply := "HTTP/1.1 200 OK\r\n" +
			"CACHE-CONTROL: max-age=1800\r\n" +
			"DATE: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n" +
			"EXT:\r\n" +
			"LOCATION: " + location + "\r\n" +
			"SERVER: Windows/10.0 UPnP/1.0 OfflineU-E2E/1.0\r\n" +
			"ST: " + st + "\r\n" +
			"USN: " + d.udn + "::" + rendererDeviceType + "\r\n" +
			"\r\n"
		if _, err := connection.WriteToUDP([]byte(reply), source); err != nil {
			log.Printf("ssdp: reply: %v", err)
			continue
		}
		d.record("SSDP", "", st)
	}
}

// headerValue reads one header out of a raw HTTP datagram.
func headerValue(message, name string) string {
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// handleControl answers the AVTransport SOAP actions.
func (d *device) handleControl(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	action := soapAction(r.Header.Get("SOAPACTION"), string(body))
	switch action {
	case "SetAVTransportURI":
		uri := between(string(body), "<CurrentURI>", "</CurrentURI>")
		d.mu.Lock()
		d.uri = uri
		if d.state == "STOPPED" {
			d.offset = 0
		}
		d.mu.Unlock()
		d.record(action, uri, "")
		soapReply(w, action, "")
	case "Play":
		d.mu.Lock()
		d.state = "PLAYING"
		d.playAt = time.Now()
		d.mu.Unlock()
		d.record(action, d.uri, "")
		soapReply(w, action, "")
	case "Pause":
		d.mu.Lock()
		if d.state == "PLAYING" {
			d.offset = d.positionLocked()
			d.state = "PAUSED_PLAYBACK"
		}
		d.mu.Unlock()
		d.record(action, d.uri, "")
		soapReply(w, action, "")
	case "Stop":
		d.mu.Lock()
		if d.state == "PLAYING" {
			d.offset = d.positionLocked()
		}
		d.state = "STOPPED"
		d.mu.Unlock()
		d.record(action, d.uri, "")
		soapReply(w, action, "")
	case "Seek":
		target := between(string(body), "<Target>", "</Target>")
		seconds := parseClock(target)
		d.mu.Lock()
		d.offset = time.Duration(seconds * float64(time.Second))
		d.playAt = time.Now()
		d.mu.Unlock()
		d.record(action, d.uri, target)
		soapReply(w, action, "")
	case "GetPositionInfo":
		d.mu.Lock()
		position := d.positionSecondsLocked()
		uri := d.uri
		state := d.state
		d.mu.Unlock()
		d.record(action, uri, state)
		soapReply(w, action,
			"<Track>1</Track>"+
				"<TrackDuration>"+clock(d.duration.Seconds())+"</TrackDuration>"+
				"<TrackURI>"+xmlEscape(uri)+"</TrackURI>"+
				"<RelTime>"+clock(position)+"</RelTime>"+
				"<AbsTime>"+clock(position)+"</AbsTime>"+
				"<RelCount>0</RelCount><AbsCount>0</AbsCount>"+
				"<TransportState>"+state+"</TransportState>")
	case "GetTransportInfo":
		state := d.transportState()
		d.record(action, d.uri, state)
		soapReply(w, action,
			"<CurrentTransportState>"+state+"</CurrentTransportState>"+
				"<CurrentTransportStatus>OK</CurrentTransportStatus>"+
				"<CurrentSpeed>1</CurrentSpeed>")
	default:
		d.record(action, d.uri, "unsupported")
		soapFault(w, "401", "Invalid Action")
	}
}

// soapReply writes a successful SOAP response; extra holds the result elements.
func soapReply(w http.ResponseWriter, action, extra string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body><u:%sResponse xmlns:u="%s">%s</u:%sResponse></s:Body>
</s:Envelope>`, action, avTransportService, extra, action)
}

func soapFault(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>
  <detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%s</errorCode><errorDescription>%s</errorDescription></UPnPError></detail>
  </s:Fault></s:Body>
</s:Envelope>`, code, description)
}

// soapAction picks the action name out of the SOAPACTION header (preferred) or
// the request body.
func soapAction(header, body string) string {
	if header != "" {
		if index := strings.Index(header, "#"); index >= 0 {
			return strings.Trim(header[index+1:], `"'`)
		}
	}
	if index := strings.Index(body, "<u:"); index >= 0 {
		rest := body[index+3:]
		if end := strings.IndexAny(rest, " \t\r\n>"); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

// between extracts the text between two XML tags.
func between(payload, opening, closing string) string {
	start := strings.Index(payload, opening)
	if start < 0 {
		return ""
	}
	rest := payload[start+len(opening):]
	end := strings.Index(rest, closing)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// parseClock reads H:MM:SS[.mmm] into seconds.
func parseClock(value string) float64 {
	total := 0.0
	for _, part := range strings.Split(strings.TrimSpace(value), ":") {
		number := 0.0
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%g", &number); err != nil {
			return total
		}
		total = total*60 + number
	}
	return total
}

func xmlEscape(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '<':
			builder.WriteString("&lt;")
		case '>':
			builder.WriteString("&gt;")
		case '&':
			builder.WriteString("&amp;")
		case '"':
			builder.WriteString("&quot;")
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
