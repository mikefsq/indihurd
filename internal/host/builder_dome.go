package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/dome"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-dome", buildDome) }

func buildDome(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, dome.Validate)
	num, err := e.Number("dome")
	if err != nil {
		return nil, err
	}
	dev := dome.New(dome.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.DomeType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
