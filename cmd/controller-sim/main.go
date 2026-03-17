package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"tcpadapter/internal/protocol"
)

var errSimulatedReconnect = errors.New("simulated reconnect")

func main() {
	addr := flag.String("addr", "127.0.0.1:15010", "tcpadapter address")
	clients := flag.Int("clients", 5, "number of simulated controllers")
	interval := flag.Duration("status-interval", 5*time.Second, "base status1 send interval")
	baseIMEI := flag.Int64("base-imei", 860000000000000, "base IMEI number")
	ackDelay := flag.Duration("ack-delay", 0, "base artificial delay before ack11")
	ackErrorRate := flag.Float64("ack-error-rate", 0.0, "base probability [0..1] to send non-zero ack code")
	ackDropRate := flag.Float64("ack-drop-rate", 0.0, "base probability [0..1] to skip ack")
	burstSize := flag.Int("status-burst-size", 1, "base number of status1 packets per tick")
	burstSpacing := flag.Duration("status-burst-spacing", 50*time.Millisecond, "base spacing between packets inside burst")
	profile := flag.String("profile", "healthy", "simulator profile: healthy|slow|flaky|bursty|starved")
	profileMix := flag.String("profile-mix", "", "comma-separated profile cycle for clients, e.g. healthy,flaky,bursty")
	frameModeName := flag.String("frame-mode", "compact", "frame mode: compact|sequenced")
	flag.Parse()

	if *clients <= 0 {
		log.Fatal("clients must be > 0")
	}
	if *ackErrorRate < 0 || *ackErrorRate > 1 {
		log.Fatal("ack-error-rate must be within [0..1]")
	}
	if *ackDropRate < 0 || *ackDropRate > 1 {
		log.Fatal("ack-drop-rate must be within [0..1]")
	}
	if *burstSize <= 0 {
		log.Fatal("status-burst-size must be > 0")
	}

	profiles, err := resolveProfileCycle(*profile, *profileMix)
	if err != nil {
		log.Fatal(err)
	}
	frameMode, err := parseFrameMode(*frameModeName)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	baseCfg := simConfig{
		profileName:         *profile,
		ackDelay:            *ackDelay,
		ackErrorRate:        *ackErrorRate,
		ackDropRate:         *ackDropRate,
		burstSize:           *burstSize,
		burstSpacing:        *burstSpacing,
		statusInterval:      *interval,
		bufferFree:          1500,
		reconnectMin:        500 * time.Millisecond,
		reconnectMax:        1500 * time.Millisecond,
		postCommandCoolDown: 250 * time.Millisecond,
		frameMode:           frameMode,
	}

	log.Printf(
		"controller-sim start addr=%s clients=%d profiles=%s ack_delay=%s ack_err=%.2f ack_drop=%.2f burst=%d",
		*addr, *clients, strings.Join(profiles, ","), baseCfg.ackDelay, baseCfg.ackErrorRate, baseCfg.ackDropRate, baseCfg.burstSize,
	)

	var wg sync.WaitGroup
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		imei := fmt.Sprintf("%015d", *baseIMEI+int64(i))
		controllerCfg, err := applyProfile(baseCfg, profiles[i%len(profiles)])
		if err != nil {
			log.Fatal(err)
		}
		go func(controllerID string, cfg simConfig) {
			defer wg.Done()
			runController(ctx.Done(), *addr, controllerID, cfg)
		}(imei, controllerCfg)
	}
	<-ctx.Done()
	wg.Wait()
	log.Printf("controller-sim stopped")
}

type simConfig struct {
	profileName         string
	ackDelay            time.Duration
	ackErrorRate        float64
	ackDropRate         float64
	burstSize           int
	burstSpacing        time.Duration
	statusInterval      time.Duration
	bufferFree          uint16
	reconnectMin        time.Duration
	reconnectMax        time.Duration
	postCommandCoolDown time.Duration
	frameMode           protocol.FrameMode
}

type controllerState struct {
	bufferFree uint16
	triggers   protocol.Cmd5TriggerFlags
}

func newControllerState(bufferFree uint16) *controllerState {
	return &controllerState{bufferFree: bufferFree}
}

func (s *controllerState) reset() {
	s.triggers = protocol.Cmd5TriggerFlags{}
}

func (s *controllerState) applyTriggerPayload(payload []byte) error {
	if err := protocol.ValidateServerCommandPayload(protocol.CmdSetTriggers, payload); err != nil {
		return err
	}
	s.triggers = protocol.Cmd5TriggerFlags{
		ManualUnlock:        payload[0] == 1,
		ScenarioUnlock:      payload[1] == 1,
		AutoRegistration:    payload[2] == 1,
		OpenForAll:          payload[3] == 1,
		ForwardSMS:          payload[4] == 1,
		NotifyUnknownIDSMS:  payload[5] == 1,
		NotifyLowBalanceSMS: payload[6] == 1,
		ScheduleForAllCalls: payload[7] == 1,
	}
	return nil
}

func resolveProfileCycle(primary, mix string) ([]string, error) {
	if strings.TrimSpace(mix) == "" {
		if _, err := applyProfile(simConfig{}, primary); err != nil {
			return nil, err
		}
		return []string{strings.TrimSpace(primary)}, nil
	}
	parts := strings.Split(mix, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, err := applyProfile(simConfig{}, name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("profile-mix must contain at least one valid profile")
	}
	return out, nil
}

func parseFrameMode(name string) (protocol.FrameMode, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "", "compact":
		return protocol.FrameModeCompact, nil
	case "sequenced":
		return protocol.FrameModeSequenced, nil
	default:
		return protocol.FrameModeAuto, fmt.Errorf("unknown frame mode %q", name)
	}
}

func applyProfile(base simConfig, profileName string) (simConfig, error) {
	cfg := base
	cfg.profileName = strings.TrimSpace(profileName)
	switch cfg.profileName {
	case "", "healthy":
		if cfg.statusInterval <= 0 {
			cfg.statusInterval = 5 * time.Second
		}
		if cfg.bufferFree == 0 {
			cfg.bufferFree = 1500
		}
		if cfg.burstSize <= 0 {
			cfg.burstSize = 1
		}
		return cfg, nil
	case "slow":
		cfg.ackDelay += 1500 * time.Millisecond
		cfg.statusInterval = maxDuration(cfg.statusInterval, 8*time.Second)
		cfg.bufferFree = 1200
		return cfg, nil
	case "flaky":
		cfg.ackDelay += 400 * time.Millisecond
		cfg.ackErrorRate = clamp01(maxFloat(cfg.ackErrorRate, 0.15))
		cfg.ackDropRate = clamp01(maxFloat(cfg.ackDropRate, 0.20))
		cfg.bufferFree = 900
		return cfg, nil
	case "bursty":
		cfg.burstSize = maxInt(cfg.burstSize, 4)
		if cfg.burstSpacing <= 0 || cfg.burstSpacing > 25*time.Millisecond {
			cfg.burstSpacing = 25 * time.Millisecond
		}
		if cfg.statusInterval <= 0 || cfg.statusInterval > 2*time.Second {
			cfg.statusInterval = 2 * time.Second
		}
		cfg.bufferFree = 1400
		return cfg, nil
	case "starved":
		cfg.ackDelay += 250 * time.Millisecond
		cfg.bufferFree = 96
		cfg.statusInterval = maxDuration(cfg.statusInterval, 6*time.Second)
		return cfg, nil
	default:
		return simConfig{}, fmt.Errorf("unknown profile %q", profileName)
	}
}

func runController(done <-chan struct{}, addr, imei string, cfg simConfig) {
	state := newControllerState(cfg.bufferFree)
	for {
		select {
		case <-done:
			return
		default:
		}

		conn, err := net.Dial("tcp", addr)
		if err != nil {
			log.Printf("[%s][%s] dial error: %v", imei, cfg.profileName, err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := serveConn(done, conn, imei, cfg, state); err != nil && !errors.Is(err, errSimulatedReconnect) {
			log.Printf("[%s][%s] connection closed: %v", imei, cfg.profileName, err)
		}
		_ = conn.Close()
		time.Sleep(randomJitter(cfg.reconnectMin, cfg.reconnectMax))
	}
}

func serveConn(done <-chan struct{}, conn net.Conn, imei string, cfg simConfig, state *controllerState) error {
	reader := bufio.NewReader(conn)
	var writeMu sync.Mutex

	if err := writeFrame(conn, &writeMu, cfg.frameMode, buildRegistrationPayload(imei)); err != nil {
		return err
	}

	ticker := time.NewTicker(cfg.statusInterval)
	defer ticker.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			frame, err := protocol.ReadFrameWithMode(reader, protocol.FrameModeAuto)
			if err != nil {
				errCh <- err
				return
			}
			cmdID, ok := frame.CommandID()
			if !ok {
				continue
			}
			if cmdID == protocol.CmdControllerAck || cmdID == protocol.CmdRegistrationAck {
				continue
			}
			disconnect, err := handleServerCommand(conn, &writeMu, frame, state, cfg)
			if err != nil {
				errCh <- err
				return
			}
			if disconnect {
				errCh <- errSimulatedReconnect
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return nil
		case err := <-errCh:
			return err
		case <-ticker.C:
			for i := 0; i < cfg.burstSize; i++ {
				if err := writeFrame(conn, &writeMu, cfg.frameMode, buildStatus1Payload(state.bufferFree, 0)); err != nil {
					return err
				}
				if i < cfg.burstSize-1 && cfg.burstSpacing > 0 {
					time.Sleep(cfg.burstSpacing)
				}
			}
		}
	}
}

func handleServerCommand(conn net.Conn, mu *sync.Mutex, frame protocol.Frame, state *controllerState, cfg simConfig) (bool, error) {
	cmdID := frame.Payload[0]
	payload := frame.Payload[1:]

	switch cmdID {
	case protocol.CmdReboot:
		timeout := uint8(0)
		if len(payload) > 0 {
			timeout = payload[0]
		}
		if err := sendCommandAck(conn, mu, frame.Seq, protocol.AckCodeStarted, state.bufferFree, cfg); err != nil {
			return false, err
		}
		time.Sleep(commandDelay(timeout, cfg))
		if err := sendCommandAck(conn, mu, frame.Seq, chooseTerminalAckCode(cfg), state.bufferFree, cfg); err != nil {
			return false, err
		}
		time.Sleep(cfg.postCommandCoolDown)
		return true, nil
	case protocol.CmdFactoryReset:
		timeout := uint8(0)
		if len(payload) > 0 {
			timeout = payload[0]
		}
		if err := sendCommandAck(conn, mu, frame.Seq, protocol.AckCodeStarted, state.bufferFree, cfg); err != nil {
			return false, err
		}
		time.Sleep(commandDelay(timeout, cfg))
		if err := sendCommandAck(conn, mu, frame.Seq, chooseTerminalAckCode(cfg), state.bufferFree, cfg); err != nil {
			return false, err
		}
		state.reset()
		time.Sleep(cfg.postCommandCoolDown)
		return true, nil
	case protocol.CmdSetTriggers:
		if err := state.applyTriggerPayload(payload); err != nil {
			return false, err
		}
		return false, sendCommandAck(conn, mu, frame.Seq, chooseTerminalAckCode(cfg), state.bufferFree, cfg)
	default:
		return false, sendCommandAck(conn, mu, frame.Seq, chooseTerminalAckCode(cfg), state.bufferFree, cfg)
	}
}

func chooseTerminalAckCode(cfg simConfig) uint8 {
	if rand.Float64() < cfg.ackErrorRate {
		return protocol.AckCodeExecError
	}
	return protocol.AckCodeOK
}

func sendCommandAck(conn net.Conn, mu *sync.Mutex, seq, code uint8, bufferFree uint16, cfg simConfig) error {
	if code != protocol.AckCodeStarted && rand.Float64() < cfg.ackDropRate {
		return nil
	}
	if cfg.ackDelay > 0 {
		time.Sleep(cfg.ackDelay)
	}
	return writeFrame(conn, mu, cfg.frameMode, buildAck11Payload(seq, code, bufferFree))
}

func commandDelay(timeout uint8, cfg simConfig) time.Duration {
	if timeout == 0 {
		if cfg.ackDelay > 0 {
			return cfg.ackDelay
		}
		return 250 * time.Millisecond
	}
	return time.Duration(timeout) * time.Second
}

func writeFrame(conn net.Conn, mu *sync.Mutex, mode protocol.FrameMode, payload []byte) error {
	wire, err := protocol.EncodeFrame(protocol.Frame{TTL: 255, Seq: 1, Payload: payload, Mode: mode})
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write(wire)
	return err
}

func buildRegistrationPayload(imei string) []byte {
	b := make([]byte, 0, 18)
	b = append(b, 1)
	imeiBytes := []byte(imei)
	if len(imeiBytes) > 15 {
		imeiBytes = imeiBytes[:15]
	}
	pad := make([]byte, 15)
	copy(pad, imeiBytes)
	b = append(b, pad...)
	b = append(b, 1)
	b = append(b, 1)
	return b
}

func buildStatus1Payload(bufferFree uint16, flags uint16) []byte {
	b := make([]byte, 9)
	b[0] = 2
	binary.LittleEndian.PutUint32(b[1:5], uint32(time.Now().Unix()))
	binary.LittleEndian.PutUint16(b[5:7], flags)
	binary.LittleEndian.PutUint16(b[7:9], bufferFree)
	return b
}

func buildAck11Payload(seq, code uint8, bufferFree uint16) []byte {
	b := make([]byte, 9)
	b[0] = 11
	b[1] = seq
	b[2] = code
	binary.LittleEndian.PutUint32(b[3:7], uint32(time.Now().Unix()))
	binary.LittleEndian.PutUint16(b[7:9], bufferFree)
	return b
}

func randomJitter(min, max time.Duration) time.Duration {
	if min <= 0 && max <= 0 {
		return 0
	}
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
