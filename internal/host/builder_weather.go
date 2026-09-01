package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/weather"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-weather", buildWeather) }

func buildWeather(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, weather.Validate)
	num, err := e.Number("observingconditions")
	if err != nil {
		return nil, err
	}
	dev := weather.New(weather.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.ObservingConditionsType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
