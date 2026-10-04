// Package offlineu implements the self-hosted, offline course viewer and
// progress tracker that powers the OfflineU web application.
//
// This file adds UPnP/DLNA casting: OfflineU searches the local network for
// media renderers (TVs, speakers, Kodi, …), hands them the URL of a lesson's
// media file and asks them to play it. Everything is done with the standard
// library - SSDP over UDP, the device description XML and SOAP over HTTP.
package offlineu

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Constants that tune discovery and control of UPnP/DLNA renderers.
const (
	// The SSDP multicast group every UPnP device listens on.
	ssdpMulticastAddress = "239.255.255.250:1900"
	// The AVTransport service is what accepts "play this URL" requests.
	avTransportService = "urn:schemas-upnp-org:service:AVTransport:1"
	// Search targets: renderers answer the first one, the second catches the
	// devices that ignore it (some smart speakers only answer "ssdp:all").
	rendererSearchTarget = "urn:schemas-upnp-org:device:MediaRenderer:1"
	allSearchTarget      = "ssdp:all"

	dlnaSearchWait         = 2 * time.Second // how long to listen for SSDP replies (MX)
	dlnaDescriptionTimeout = 4 * time.Second // per device description download
	dlnaActionTimeout      = 8 * time.Second // per SOAP call
	dlnaMaxDescriptionJobs = 8               // parallel description downloads
	dlnaMaxDevices         = 32              // hard cap, a huge LAN cannot stall us
	dlnaCacheTTL           = 30 * time.Second
)

// Renderer is one device OfflineU can push a lesson to.
type Renderer struct {
	UDN          string `json:"udn"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Address      string `json:"address"`
	Location     string `json:"location"`
	ControlURL   string `json:"control_url"`
	ServiceType  string `json:"service_type"`
}

// DisplayName is what the UI prints: the friendly name, or the address when a
// device did not bother to send one.
func (r Renderer) DisplayName() string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	if strings.TrimSpace(r.Address) != "" {
		return r.Address
	}
	return r.UDN
}

// upnpService is one <service> entry of a device description.
type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ServiceID   string `xml:"serviceId"`
	ControlURL  string `xml:"controlURL"`
}

// upnpDevice mirrors the <device> element; nested <deviceList> entries are
// kept because renderers often hide AVTransport inside an embedded device.
type upnpDevice struct {
	DeviceType   string        `xml:"deviceType"`
	FriendlyName string        `xml:"friendlyName"`
	Manufacturer string        `xml:"manufacturer"`
	ModelName    string        `xml:"modelName"`
	UDN          string        `xml:"UDN"`
	Services     []upnpService `xml:"serviceList>service"`
	Devices      []upnpDevice  `xml:"deviceList>device"`
}

// upnpRoot is the device description document.
type upnpRoot struct {
	XMLName xml.Name   `xml:"root"`
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

// DLNAHub discovers renderers and caches the result, so opening the cast menu
// does not repeat a two second SSDP scan on every click.
type DLNAHub struct {
	mu         sync.Mutex
	client     *http.Client
	renderers  []Renderer
	fetchedAt  time.Time
	ttl        time.Duration
	searchWait time.Duration
}

// NewDLNAHub builds the hub used by the HTTP layer.
func NewDLNAHub() *DLNAHub {
	return &DLNAHub{
		client:     &http.Client{Timeout: dlnaActionTimeout},
		ttl:        dlnaCacheTTL,
		searchWait: dlnaSearchWait,
	}
}

// Devices lists the renderers currently known. force repeats the SSDP search
// even when a cached result is still fresh (the ⟳ button of the cast menu).
func (h *DLNAHub) Devices(force bool) ([]Renderer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ttl <= 0 {
		h.ttl = dlnaCacheTTL
	}

	if !force && len(h.renderers) > 0 && time.Since(h.fetchedAt) < h.ttl {
		return append([]Renderer{}, h.renderers...), nil
	}

	found, err := discoverRenderers(h.searchWait)
	if err != nil {
		return nil, err
	}
	switch {
	case len(found) > 0:
		h.renderers = found
		h.fetchedAt = time.Now()
	case len(h.renderers) == 0:
		// Nothing found and nothing cached: remember the empty result so a
		// page without any renderer does not rescan on every request.
		h.renderers = []Renderer{}
		h.fetchedAt = time.Now()
	default:
		// A renderer was known but went to sleep: keep it listed.
		h.fetchedAt = time.Now()
		return append([]Renderer{}, h.renderers...), nil
	}
	return append([]Renderer{}, h.renderers...), nil
}

// Lookup finds a cached renderer by its UDN, refreshing the list once when the
// device is not in it yet (it may have appeared after the last scan).
func (h *DLNAHub) Lookup(udn string) (Renderer, bool) {
	wanted := strings.TrimSpace(udn)
	if wanted == "" {
		return Renderer{}, false
	}
	devices, err := h.Devices(false)
	if err == nil {
		for _, device := range devices {
			if device.UDN == wanted {
				return device, true
			}
		}
	}
	if refreshed, err := h.Devices(true); err == nil {
		for _, device := range refreshed {
			if device.UDN == wanted {
				return device, true
			}
		}
	}
	return Renderer{}, false
}

// MediaItem is the lesson file handed to a renderer.
type MediaItem struct {
	Title string // shown on the TV
	URL   string // absolute URL the renderer fetches
	Mime  string // e.g. "video/mp4"
	Class string // UPnP class: object.item.videoItem / object.item.audioItem.musicTrack
}

// Cast points a renderer at item and starts playback. startSeconds is applied
// best effort: renderers that refuse a seek simply play from the beginning.
func (h *DLNAHub) Cast(renderer Renderer, item MediaItem, startSeconds int) error {
	err := renderer.call(h.client, "SetAVTransportURI", [][2]string{
		{"InstanceID", "0"},
		{"CurrentURI", item.URL},
		{"CurrentURIMetaData", didlMetadata(item)},
	})
	if err != nil {
		return fmt.Errorf("%s did not accept the media: %w", renderer.DisplayName(), err)
	}
	if err := renderer.call(h.client, "Play", [][2]string{
		{"InstanceID", "0"},
		{"Speed", "1"},
	}); err != nil {
		return fmt.Errorf("%s did not start playing: %w", renderer.DisplayName(), err)
	}
	if startSeconds > 0 {
		if err := renderer.seek(h.client, startSeconds); err != nil {
			// Not fatal: the renderer is already playing, just from 0:00.
			logf("dlna: %s refused the seek to %ds: %v", renderer.DisplayName(), startSeconds, err)
		}
	}
	return nil
}

// Seek jumps inside the lesson that is playing. Renderers that cannot seek say
// so with a UPnP error, which the caller turns into a message for the UI.
func (h *DLNAHub) Seek(renderer Renderer, seconds int) error {
	client := h.client
	if client == nil {
		client = &http.Client{Timeout: dlnaActionTimeout}
	}
	return renderer.seek(client, seconds)
}

// seek is the Seek action of the AVTransport service.
func (r Renderer) seek(client *http.Client, seconds int) error {
	if seconds < 0 {
		seconds = 0
	}
	return r.call(client, "Seek", [][2]string{
		{"InstanceID", "0"},
		{"Unit", "REL_TIME"},
		{"Target", formatUPnPDuration(seconds)},
	})
}

// Control sends a transport action ("Play", "Pause" or "Stop") to a renderer.
func (h *DLNAHub) Control(renderer Renderer, action string) error {
	switch action {
	case "play", "pause", "stop":
	default:
		return fmt.Errorf("unknown dlna action %q", action)
	}
	soapAction := strings.ToUpper(action[:1]) + action[1:]
	args := [][2]string{{"InstanceID", "0"}}
	if soapAction == "Play" {
		args = append(args, [2]string{"Speed", "1"})
	}
	if err := renderer.call(h.client, soapAction, args); err != nil {
		return fmt.Errorf("%s did not answer %s: %w", renderer.DisplayName(), soapAction, err)
	}
	return nil
}

// discoverRenderers runs one SSDP search and describes everything that replied.
func discoverRenderers(wait time.Duration) ([]Renderer, error) {
	locations, err := ssdpSearch(wait)
	if err != nil {
		return nil, err
	}
	renderers, err := describeDevices(locations)
	if err != nil {
		return nil, err
	}
	return renderers, nil
}

// ssdpSearch broadcasts M-SEARCH and collects the LOCATION header of every
// reply. It needs no multicast group membership: devices answer straight to the
// port the datagram was sent from.
func ssdpSearch(wait time.Duration) ([]string, error) {
	if wait <= 0 {
		wait = dlnaSearchWait
	}
	connection, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("cannot open a UDP socket for the network search: %w", err)
	}
	defer connection.Close()

	target, err := net.ResolveUDPAddr("udp4", ssdpMulticastAddress)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the SSDP multicast address: %w", err)
	}
	seconds := int(wait.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	for _, searchTarget := range []string{rendererSearchTarget, allSearchTarget} {
		message := "M-SEARCH * HTTP/1.1\r\n" +
			"HOST: " + ssdpMulticastAddress + "\r\n" +
			`MAN: "ssdp:discover"` + "\r\n" +
			"MX: " + strconv.Itoa(seconds) + "\r\n" +
			"ST: " + searchTarget + "\r\n" +
			"\r\n"
		if _, err := connection.WriteTo([]byte(message), target); err != nil {
			return nil, fmt.Errorf("cannot send the network search: %w", err)
		}
	}

	deadline := time.Now().Add(wait + 500*time.Millisecond)
	seen := map[string]bool{}
	locations := []string{}
	buffer := make([]byte, 65535)
	for {
		if time.Now().After(deadline) {
			break
		}
		if err := connection.SetReadDeadline(deadline); err != nil {
			break
		}
		count, _, err := connection.ReadFrom(buffer)
		if err != nil {
			break // read deadline reached: every device had its chance
		}
		for _, line := range strings.Split(string(buffer[:count]), "\n") {
			line = strings.TrimRight(line, "\r")
			if !strings.HasPrefix(strings.ToLower(line), "location:") {
				continue
			}
			value := strings.TrimSpace(line[len("location:"):])
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			locations = append(locations, value)
		}
	}
	return locations, nil
}

// describeDevices downloads every device description in parallel and keeps the
// devices that really expose an AVTransport service.
func describeDevices(locations []string) ([]Renderer, error) {
	renderers := []Renderer{}
	if len(locations) == 0 {
		return renderers, nil
	}
	if len(locations) > dlnaMaxDevices {
		locations = locations[:dlnaMaxDevices]
	}

	client := &http.Client{Timeout: dlnaDescriptionTimeout}
	var waiter sync.WaitGroup
	var mutex sync.Mutex
	slots := make(chan struct{}, dlnaMaxDescriptionJobs)
	for _, location := range locations {
		waiter.Add(1)
		go func(location string) {
			defer waiter.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			renderer, err := fetchRenderer(client, location)
			if err != nil {
				logf("dlna: skipping %s: %v", location, err)
				return
			}
			mutex.Lock()
			renderers = append(renderers, *renderer)
			mutex.Unlock()
		}(location)
	}
	waiter.Wait()

	sort.SliceStable(renderers, func(i, j int) bool {
		return compareNames(renderers[i].DisplayName(), renderers[j].DisplayName()) < 0
	})
	return renderers, nil
}

// fetchRenderer reads one device description and turns it into a Renderer.
func fetchRenderer(client *http.Client, location string) (*Renderer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dlnaDescriptionTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}

	var root upnpRoot
	decoder := xml.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.Strict = false
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("cannot read the device description: %w", err)
	}
	service, owner, ok := findAVTransport(root.Device)
	if !ok {
		return nil, errors.New("no AVTransport service (not a media renderer)")
	}

	base, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if trimmed := strings.TrimSpace(root.URLBase); trimmed != "" {
		if parsed, err := url.Parse(trimmed); err == nil {
			base = parsed
		}
	}
	control, err := base.Parse(service.ControlURL)
	if err != nil {
		return nil, fmt.Errorf("invalid control URL %q: %w", service.ControlURL, err)
	}
	name := strings.TrimSpace(owner.FriendlyName)
	if name == "" {
		name = strings.TrimSpace(root.Device.FriendlyName)
	}
	udn := strings.TrimSpace(root.Device.UDN)
	if udn == "" {
		udn = strings.TrimSpace(owner.UDN)
	}
	serviceType := strings.TrimSpace(service.ServiceType)
	if serviceType == "" {
		serviceType = avTransportService
	}
	return &Renderer{
		UDN:          udn,
		Name:         name,
		Manufacturer: strings.TrimSpace(owner.Manufacturer),
		Model:        strings.TrimSpace(owner.ModelName),
		Address:      control.Hostname(),
		Location:     location,
		ControlURL:   control.String(),
		ServiceType:  serviceType,
	}, nil
}

// findAVTransport walks a device and its embedded devices looking for the
// service that accepts "play this URL". The owning device is returned too,
// because its friendly name is the one worth showing in the UI.
func findAVTransport(device upnpDevice) (upnpService, upnpDevice, bool) {
	for _, service := range device.Services {
		if strings.Contains(strings.ToLower(service.ServiceType), "avtransport") {
			return service, device, true
		}
	}
	for _, child := range device.Devices {
		if service, owner, ok := findAVTransport(child); ok {
			return service, owner, true
		}
	}
	return upnpService{}, device, false
}

// didlMetadata builds the DIDL-Lite description most renderers expect next to
// the URL: without it a TV often shows "unknown" instead of the lesson title.
func didlMetadata(item MediaItem) string {
	class := item.Class
	if class == "" {
		class = "object.item.videoItem"
	}
	mime := item.Mime
	if mime == "" {
		mime = "video/mp4"
	}
	var builder strings.Builder
	builder.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)
	builder.WriteString(`<item id="offlineu-0" parentID="-1" restricted="1">`)
	builder.WriteString(`<dc:title>` + escapeXML(item.Title) + `</dc:title>`)
	builder.WriteString(`<upnp:class>` + escapeXML(class) + `</upnp:class>`)
	builder.WriteString(`<res protocolInfo="http-get:*:` + escapeXML(mime) + `:*">` +
		escapeXML(item.URL) + `</res>`)
	builder.WriteString(`</item></DIDL-Lite>`)
	return builder.String()
}

// PositionInfo is the answer to GetPositionInfo: where the renderer really is
// and what it is doing. A remote control in the living room changes both
// without OfflineU noticing, so the cast session asks instead of guessing.
type PositionInfo struct {
	TrackDuration  float64 `json:"track_duration"`
	RelTime        float64 `json:"rel_time"`
	TransportState string  `json:"transport_state"`
}

type positionInfoResponse struct {
	TrackDuration  string `xml:"TrackDuration"`
	RelTime        string `xml:"RelTime"`
	AbsTime        string `xml:"AbsTime"`
	TransportState string `xml:"TransportState"`
}

type soapResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		PositionInfo *positionInfoResponse `xml:"GetPositionInfoResponse"`
	} `xml:"Body"`
}

// Position asks a renderer where it is. It is best effort: plenty of devices
// answer "NOT_IMPLEMENTED" or refuse the action, and the caller then falls back
// to the time based estimate.
func (h *DLNAHub) Position(renderer Renderer) (PositionInfo, error) {
	client := h.client
	if client == nil {
		client = &http.Client{Timeout: dlnaActionTimeout}
	}
	body, err := renderer.callWithBody(client, "GetPositionInfo", [][2]string{{"InstanceID", "0"}})
	if err != nil {
		return PositionInfo{}, err
	}
	var decoded soapResponse
	if err := xml.Unmarshal([]byte(body), &decoded); err != nil {
		return PositionInfo{}, fmt.Errorf("cannot read the position answer: %w", err)
	}
	if decoded.Body.PositionInfo == nil {
		return PositionInfo{}, errors.New("the renderer sent no position")
	}
	position := *decoded.Body.PositionInfo
	info := PositionInfo{
		TrackDuration:  parseClockSeconds(position.TrackDuration),
		RelTime:        parseClockSeconds(position.RelTime),
		TransportState: strings.ToUpper(strings.TrimSpace(position.TransportState)),
	}
	if info.TrackDuration <= 0 && info.RelTime <= 0 && info.TransportState == "" {
		return PositionInfo{}, errors.New("the renderer reported no position")
	}
	return info, nil
}

// parseClockSeconds reads the H:MM:SS[.mmm] a renderer reports. Anything it
// cannot express ("NOT_IMPLEMENTED", "0") becomes 0 = unknown.
func parseClockSeconds(raw string) float64 {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0
	}
	// Every field is the next smaller unit: "1:02:05" -> 1*3600 + 2*60 + 5.
	total := 0.0
	for _, part := range strings.Split(value, ":") {
		number, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return 0 // "NOT_IMPLEMENTED" and friends
		}
		total = total*60 + number
	}
	return total
}

// call sends one SOAP action to the renderer's control endpoint.
func (r Renderer) call(client *http.Client, action string, args [][2]string) error {
	_, err := r.callWithBody(client, action, args)
	return err
}

// callWithBody is call() plus the response body, which GetPositionInfo needs.
func (r Renderer) callWithBody(client *http.Client, action string, args [][2]string) (string, error) {
	if strings.TrimSpace(r.ControlURL) == "" {
		return "", errors.New("the renderer has no control URL")
	}
	if client == nil {
		client = &http.Client{Timeout: dlnaActionTimeout}
	}
	body := soapEnvelope(r.ServiceType, action, args)
	ctx, cancel := context.WithTimeout(context.Background(), dlnaActionTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.ControlURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header.Set("SOAPACTION", `"`+r.ServiceType+"#"+action+`"`)

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	// Renderers report a refused action either as a SOAP fault inside a 200 or
	// as a plain 500 carrying the same fault - both are worth decoding.
	if message := soapFaultMessage(string(payload)); message != "" {
		return "", errors.New(message)
	}
	if response.StatusCode/100 != 2 {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return string(payload), nil
}

// soapEnvelope wraps the arguments of one action into a SOAP request body.
func soapEnvelope(serviceType, action string, args [][2]string) string {
	if strings.TrimSpace(serviceType) == "" {
		serviceType = avTransportService
	}
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	builder.WriteString(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`)
	builder.WriteString(`<s:Body><u:` + action + ` xmlns:u="` + serviceType + `">`)
	for _, argument := range args {
		builder.WriteString("<" + argument[0] + ">")
		builder.WriteString(escapeXML(argument[1]))
		builder.WriteString("</" + argument[0] + ">")
	}
	builder.WriteString(`</u:` + action + `></s:Body></s:Envelope>`)
	return builder.String()
}

// soapFaultMessage extracts the readable part of a UPnP error response.
func soapFaultMessage(payload string) string {
	if !strings.Contains(strings.ToLower(payload), "upnperror") {
		return ""
	}
	description := xmlTagText(payload, "errorDescription")
	code := xmlTagText(payload, "errorCode")
	switch {
	case description != "" && code != "" && code != "0":
		return fmt.Sprintf("%s (UPnP %s)", description, code)
	case description != "":
		return description
	case code != "" && code != "0":
		return "UPnP error " + code
	default:
		return "UPnP error"
	}
}

// xmlTagText returns the text of the first <tag>…</tag> occurrence.
func xmlTagText(payload, tag string) string {
	opening := "<" + tag
	index := strings.Index(strings.ToLower(payload), strings.ToLower(opening))
	if index < 0 {
		return ""
	}
	rest := payload[index+len(opening):]
	if end := strings.Index(rest, ">"); end >= 0 {
		rest = rest[end+1:]
	}
	closing := strings.Index(strings.ToLower(rest), "</"+strings.ToLower(tag)+">")
	if closing < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:closing])
}

// escapeXML makes a value safe inside an XML element.
func escapeXML(value string) string {
	var buffer bytes.Buffer
	if err := xml.EscapeText(&buffer, []byte(value)); err != nil {
		return value
	}
	return buffer.String()
}

// formatUPnPDuration renders seconds as the H:MM:SS a Seek action expects.
func formatUPnPDuration(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}
