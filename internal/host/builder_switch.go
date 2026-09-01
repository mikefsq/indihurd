package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/switchdev"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-switch", buildSwitch) }

func buildSwitch(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, switchdev.Validate)
	num, err := e.Number("switch")
	if err != nil {
		return nil, err
	}
	dev := switchdev.New(switchdev.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.SwitchType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
