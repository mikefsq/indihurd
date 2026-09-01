package host

import (
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/devtype/covercal"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-covercal", buildCoverCal) }

func buildCoverCal(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	sup, kit := assemble(e, logf, covercal.Validate)
	num, err := e.Number("covercalibrator")
	if err != nil {
		return nil, err
	}
	dev := covercal.New(covercal.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.CoverCalibratorType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
