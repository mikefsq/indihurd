package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/telescope"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-telescope", buildTelescope) }

func buildTelescope(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, telescope.Validate)
	num, err := e.Number("telescope")
	if err != nil {
		return nil, err
	}
	dev := telescope.New(telescope.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.TelescopeType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
