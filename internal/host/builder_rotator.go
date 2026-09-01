package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/rotator"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-rotator", buildRotator) }

func buildRotator(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, rotator.Validate)
	num, err := e.Number("rotator")
	if err != nil {
		return nil, err
	}
	dev := rotator.New(rotator.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.RotatorType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
