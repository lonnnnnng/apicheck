package main

import (
	"apicheck/internal/checker"
	"fmt"
	"strings"
)

type protocolFlag checker.Protocol

func (p *protocolFlag) String() string {
	if *p == "" {
		return string(checker.ProtocolAuto)
	}
	return string(*p)
}

func (p *protocolFlag) Set(value string) error {
	protocol := checker.Protocol(strings.ToLower(strings.TrimSpace(value)))
	switch protocol {
	case checker.ProtocolAuto, checker.ProtocolAll, checker.ProtocolOpenAI, checker.ProtocolOpenAIChat, checker.ProtocolOpenAIResponses, checker.ProtocolAnthropic, checker.ProtocolAnthropicMessages:
		*p = protocolFlag(protocol)
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q", value)
	}
}

type modeFlag checker.Mode

func (m *modeFlag) String() string {
	if *m == "" {
		return string(checker.ModeBoth)
	}
	return string(*m)
}

func (m *modeFlag) Set(value string) error {
	mode := checker.Mode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case checker.ModeBoth, checker.ModeNonStream, checker.ModeStream:
		*m = modeFlag(mode)
		return nil
	default:
		return fmt.Errorf("unsupported mode %q", value)
	}
}

type surfaceFlag checker.ClientSurface

func (s *surfaceFlag) String() string {
	if *s == "" {
		return string(checker.ClientSurfaceCLI)
	}
	return string(*s)
}

func (s *surfaceFlag) Set(value string) error {
	surface := checker.ClientSurface(strings.ToLower(strings.TrimSpace(value)))
	switch surface {
	case checker.ClientSurfaceCLI, checker.ClientSurfaceDesktop:
		*s = surfaceFlag(surface)
		return nil
	default:
		return fmt.Errorf("unsupported client surface %q", value)
	}
}
