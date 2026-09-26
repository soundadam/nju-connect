package gatewayauth

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/soundadam/nju-connect/internal/sessiontoken"
)

const (
	resourceTypeWeb = iota
	resourceTypeTCP
	resourceTypeL3VPN
)

type ResourceSummary struct {
	Web     int
	TCP     int
	L3VPN   int
	Unknown int
}

type ServiceRequirements struct {
	TCP           bool
	L3VPN         bool
	InternalDNS   bool
	DedicatedLine bool
	SecurityCheck bool
}

func (requirements ServiceRequirements) LocalAgentRequired() bool {
	return requirements.TCP || requirements.L3VPN || requirements.InternalDNS || requirements.DedicatedLine || requirements.SecurityCheck
}

type Bootstrap struct {
	ConfigurationAvailable bool
	ResourcesAvailable     bool
	Resources              ResourceSummary
	Services               ServiceRequirements
}

// ProbeBootstrap reads only enough authenticated gateway data to decide which
// service families the Core will eventually need. Raw policy, resource names,
// addresses, and session material are discarded before this call returns.
func (session *Session) ProbeBootstrap(ctx context.Context) (Bootstrap, error) {
	if session == nil || session.http == nil {
		return Bootstrap{}, ErrNoAuthenticatedSession
	}
	clear(session.nativeGatewayToken)
	session.nativeGatewayToken = nil
	var result Bootstrap
	configuration, err := requestBytes(ctx, session.http, session.baseURL, http.MethodGet, "/por/conf.csp?apiversion=1", nil)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read gateway configuration: %w", err)
	}
	var nativeGatewayToken sessiontoken.NativeGatewayToken
	result.ConfigurationAvailable, result.Services.DedicatedLine, result.Services.SecurityCheck, nativeGatewayToken, err = inspectConfiguration(configuration)
	clear(configuration)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("inspect gateway configuration: %w", err)
	}
	transferToken := false
	defer func() {
		if !transferToken {
			clear(nativeGatewayToken)
		}
	}()

	resources, err := requestBytes(ctx, session.http, session.baseURL, http.MethodGet, "/por/rclist.csp?apiversion=1", nil)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read gateway resources: %w", err)
	}
	result.ResourcesAvailable, result.Resources, result.Services.InternalDNS, err = inspectResources(resources)
	clear(resources)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("inspect gateway resources: %w", err)
	}
	result.Services.TCP = result.Resources.TCP != 0
	result.Services.L3VPN = result.Resources.L3VPN != 0
	clear(session.nativeGatewayToken)
	session.nativeGatewayToken = nativeGatewayToken
	transferToken = true
	return result, nil
}

func inspectConfiguration(data []byte) (available, dedicatedLine, securityCheck bool, nativeGatewayToken sessiontoken.NativeGatewayToken, err error) {
	stack := make([]string, 0, 8)
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, decodeErr := decoder.Token()
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			clear(nativeGatewayToken)
			return false, false, false, nil, fmt.Errorf("decode gateway XML: %w", decodeErr)
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			if value.Name.Local == "Conf" {
				available = true
			}
			if hasPathSuffix(stack, "Conf", "SecurityCheck") {
				securityCheck = true
			}
			if hasPathSuffix(stack, "Conf", "Other") {
				if attribute, ok := xmlAttribute(value, "sddn_enable"); ok {
					dedicatedLine = numericEnabled(attribute)
				}
				if attribute, ok := xmlAttribute(value, "sslctx"); ok {
					if nativeGatewayToken != nil {
						clear(nativeGatewayToken)
						return false, false, false, nil, errors.New("gateway configuration has duplicate SSL context")
					}
					encoded := []byte(attribute)
					decoded, decodeErr := sessiontoken.DecodeNativeGatewayToken(encoded)
					clear(encoded)
					if decodeErr != nil {
						return false, false, false, nil, decodeErr
					}
					nativeGatewayToken = decoded
				}
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	return available, dedicatedLine, securityCheck, nativeGatewayToken, nil
}

func inspectResources(data []byte) (available bool, summary ResourceSummary, internalDNS bool, err error) {
	stack := make([]string, 0, 8)
	resourceDepth := 0
	resourceType := ""
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, decodeErr := decoder.Token()
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			return false, ResourceSummary{}, false, fmt.Errorf("decode gateway XML: %w", decodeErr)
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			if value.Name.Local == "Resource" {
				available = true
			}
			if hasPathSuffix(stack, "Resource", "Rcs", "Rc") {
				resourceDepth = len(stack)
				resourceType, _ = xmlAttribute(value, "type")
			}
			if hasPathSuffix(stack, "Resource", "Dns") {
				if attribute, ok := xmlAttribute(value, "dnsserver"); ok {
					internalDNS = strings.TrimSpace(attribute) != ""
				}
			}
		case xml.EndElement:
			if resourceDepth == len(stack) {
				classifyResource(&summary, resourceType)
				resourceDepth = 0
			}
			stack = stack[:len(stack)-1]
		}
	}
	return available, summary, internalDNS, nil
}

func classifyResource(summary *ResourceSummary, value string) {
	resourceType, err := strconv.Atoi(value)
	if err != nil {
		summary.Unknown++
		return
	}
	switch resourceType {
	case resourceTypeWeb:
		summary.Web++
	case resourceTypeTCP:
		summary.TCP++
	case resourceTypeL3VPN:
		summary.L3VPN++
	default:
		summary.Unknown++
	}
}

func numericEnabled(value string) bool {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && number != 0
}

func hasPathSuffix(path []string, suffix ...string) bool {
	if len(path) < len(suffix) {
		return false
	}
	start := len(path) - len(suffix)
	for index := range suffix {
		if path[start+index] != suffix[index] {
			return false
		}
	}
	return true
}

func xmlAttribute(element xml.StartElement, name string) (string, bool) {
	for _, attribute := range element.Attr {
		if attribute.Name.Local == name {
			return attribute.Value, true
		}
	}
	return "", false
}
