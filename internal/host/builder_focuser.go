package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/focuser"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-focuser", buildFocuser) }

func buildFocuser(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, focuser.Validate)
	num, err := e.Number("focuser")
	if err != nil {
		return nil, err
	}
	dev := focuser.New(focuser.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.FocuserType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
