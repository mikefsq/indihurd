package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/filterwheel"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-filterwheel", buildFilterWheel) }

func buildFilterWheel(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, filterwheel.Validate)
	num, err := e.Number("filterwheel")
	if err != nil {
		return nil, err
	}
	dev := filterwheel.New(filterwheel.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.FilterWheelType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
