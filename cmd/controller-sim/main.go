package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tcpadapter/internal/protocol"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:15010", "tcpadapter address")
	clients := flag.Int("clients", 5, "number of simulated controllers")
	interval := flag.Duration("status-interval", 5*time.Second, "status1 send interval")
	baseIMEI := flag.Int64("base-imei", 860000000000000, "base IMEI number")
	ackDelay := flag.Duration("ack-delay", 0, "artificial delay before ack11")
	ackErrorRate := flag.Float64("ack-error-rate", 0.0, "probability [0..1] to send non-zero ack code")
	ackDropRate := flag.Float64("ack-drop-rate", 0.0, "probability [0..1] to skip ack")
	burstSize := flag.Int("status-burst-size", 1, "how many status1 packets to send per tick")
	burstSpacing := flag.Duration("status-burst-spacing", 50*time.Millisecond, "spacing between packets inside burst")
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := simConfig{
		ackDelay:     *ackDelay,
		ackErrorRate: *ackErrorRate,
		ackDropRate:  *ackDropRate,
		burstSize:    *burstSize,
		burstSpacing: *burstSpacing,
	}
	log.Printf(
		"controller-sim start addr=%s clients=%d ack_delay=%s ack_err=%.2f ack_drop=%.2f burst=%d",
		*addr, *clients, cfg.ackDelay, cfg.ackErrorRate, cfg.ackDropRate, cfg.burstSize,
	)
	var wg sync.WaitGroup
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		imei := fmt.Sprintf("%015d", *baseIMEI+int64(i))
		go func(controllerID string) {
			defer wg.Done()
			runController(ctx.Done(), *addr, controllerID, *interval, cfg)
		}(imei)
	}
	<-ctx.Done()
	wg.Wait()
	log.Printf("controller-sim stopped")
}

type simConfig struct {
	ackDelay     time.Duration
	ackErrorRate float64
	ackDropRate  float64
	burstSize    int
	burstSpacing time.Duration
}

func runController(done <-chan struct{}, addr, imei string, statusInterval time.Duration, cfg simConfig) {
	for {
		select {
		case <-done:
			return
		default:
		}

		conn, err := net.Dial("tcp", addr)
		if err != nil {
			log.Printf("[%s] dial error: %v", imei, err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := serveConn(done, conn, imei, statusInterval, cfg); err != nil {
			log.Printf("[%s] connection closed: %v", imei, err)
		}
		_ = conn.Close()
		time.Sleep(time.Duration(500+rand.Intn(1000)) * time.Millisecond)
	}
}

func serveConn(done <-chan struct{}, conn net.Conn, imei string, statusInterval time.Duration, cfg simConfig) error {
	reader := bufio.NewReader(conn)
	var writeMu sync.Mutex

	if err := writeFrame(conn, &writeMu, buildRegistrationPayload(imei)); err != nil {
		return err
	}

	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			frame, err := protocol.ReadFrame(reader)
			if err != nil {
				errCh <- err
				return
			}
			cmdID, ok := frame.CommandID()
			if !ok {
				continue
			}
			if cmdID == 24 {
				continue
			}
			if rand.Float64() < cfg.ackDropRate {
				continue
			}
			code := uint8(0)
			if rand.Float64() < cfg.ackErrorRate {
				code = 2
			}
			ack := buildAck11Payload(frame.Seq, code, 1500)
			if cfg.ackDelay > 0 {
				time.Sleep(cfg.ackDelay)
			}
			if err := writeFrame(conn, &writeMu, ack); err != nil {
				errCh <- err
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
				if err := writeFrame(conn, &writeMu, buildStatus1Payload(1500)); err != nil {
					return err
				}
				if i < cfg.burstSize-1 && cfg.burstSpacing > 0 {
					time.Sleep(cfg.burstSpacing)
				}
			}
		}
	}
}

func writeFrame(conn net.Conn, mu *sync.Mutex, payload []byte) error {
	wire, err := protocol.EncodeFrame(protocol.Frame{TTL: 255, Seq: 1, Payload: payload})
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
	b = append(b, 1) // protocol version
	b = append(b, 1) // hw version
	return b
}

func buildStatus1Payload(bufferFree uint16) []byte {
	b := make([]byte, 9)
	b[0] = 2
	binary.LittleEndian.PutUint32(b[1:5], uint32(time.Now().Unix()))
	binary.LittleEndian.PutUint16(b[5:7], 0)
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
