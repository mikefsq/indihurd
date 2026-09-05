//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

const weatherSim = "drivers/weather/indi_simulator_weather"

func startWeather(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-weather",
		Exec:   filepath.Join(build, weatherSim),
		Name:   "M7Weather",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Weather Simulator", StateDir: t.TempDir()},
	}
	b, err := host.Build(entry, server.Config{
		AlpacaPort: port,
		Discovery:  server.DiscoveryConfig{Mode: server.DiscoveryOff},
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Server.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("server did not stop")
		}
	})
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", port))

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			if _, ok := b.Sup.Snapshot().Vector("Weather Simulator", "WEATHER_PARAMETERS"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

func TestWeatherObservingConditions(t *testing.T) {
	url, b := startWeather(t, 47643)
	c := client.NewObservingConditions(url, 0)
	conformance.CheckCommon(t, c)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	// WEATHER_PARAMETERS only picks up the control vector on a Refresh.
	ctx := context.Background()
	if err := b.Sup.SetNumber(ctx, "Weather Simulator", "WEATHER_CONTROL",
		map[string]float64{"Temperature": 12.5, "Wind": 18, "Gust": 36}); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v, err := c.Temperature(); err == nil && v == 12.5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v, err := c.Temperature(); err != nil || v != 12.5 {
		t.Errorf("Temperature = %v, %v; want 12.5", v, err)
	}

	// The simulator's wind is kph, disclosed only in the member label; the
	// bridge must serve m/s.
	if v, err := c.WindSpeed(); err != nil || math.Abs(v-5) > 1e-6 {
		t.Errorf("WindSpeed = %v, %v; want 5 m/s from 18 kph", v, err)
	}
	if v, err := c.WindGust(); err != nil || math.Abs(v-10) > 1e-6 {
		t.Errorf("WindGust = %v, %v; want 10 m/s from 36 kph", v, err)
	}
	if v, err := c.Humidity(); err != nil || v < 0 || v > 100 {
		t.Errorf("Humidity = %v, %v", v, err)
	}
	if v, err := c.RainRate(); err != nil || v != 0 {
		t.Errorf("RainRate = %v, %v; want 0", v, err)
	}

	// Sensors the simulator does not publish answer NotImplemented, never 0.
	for name, get := range map[string]func() (float64, error){
		"Pressure":      c.Pressure,
		"CloudCover":    c.CloudCover,
		"DewPoint":      c.DewPoint,
		"SkyQuality":    c.SkyQuality,
		"WindDirection": c.WindDirection,
	} {
		if _, err := get(); !errors.Is(err, server.ErrNotImplemented) {
			t.Errorf("%s: want NotImplemented, got %v", name, err)
		}
	}

	// SensorDescription surfaces the unit-bearing INDI label.
	if d, err := c.SensorDescription("WindSpeed"); err != nil || !strings.Contains(d, "kph") {
		t.Errorf(`SensorDescription("WindSpeed") = %q, %v; want the "(kph)" label surfaced`, d, err)
	}
	if _, err := c.SensorDescription("Pressure"); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("SensorDescription of an absent sensor: want NotImplemented, got %v", err)
	}

	// TimeSinceLastUpdate reads per-member arrival, and the refresh just landed.
	if ts, err := c.TimeSinceLastUpdate("Temperature"); err != nil || ts < 0 || ts > 60 {
		t.Errorf(`TimeSinceLastUpdate("Temperature") = %v, %v`, ts, err)
	}
	if ts, err := c.TimeSinceLastUpdate(""); err != nil || ts < -1 {
		t.Errorf(`TimeSinceLastUpdate("") = %v, %v`, ts, err)
	}

	// AveragePeriod round-trips; negative is rejected.
	if err := c.SetAveragePeriod(1.0); err != nil {
		t.Errorf("SetAveragePeriod(1.0): %v", err)
	}
	if v, err := c.AveragePeriod(); err != nil || v != 1.0 {
		t.Errorf("AveragePeriod = %v, %v; want 1.0", v, err)
	}
	if err := c.SetAveragePeriod(0); err != nil {
		t.Errorf("SetAveragePeriod(0): %v", err)
	}
	if err := c.SetAveragePeriod(-1); !errors.Is(err, server.ErrInvalidValue) {
		t.Errorf("SetAveragePeriod(-1): want InvalidValue, got %v", err)
	}
}
