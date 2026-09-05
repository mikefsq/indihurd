package host

import (
	"context"
	"fmt"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/devtype/camera"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

func init() { register("indi-camera", buildCamera) }

// buildCamera wires image callbacks before the acquire loop starts.
func buildCamera(e Entry, srv *server.Server, logf func(string, ...any)) (*supervisor.Supervisor, error) {
	var (
		sup *supervisor.Supervisor
		kit *binding.Kit
		dev *camera.Camera
	)

	deviceOf := func(snap *snapshot.Snapshot) string {
		if e.Indi.DeviceName != "" {
			return e.Indi.DeviceName
		}
		if ds := snap.Devices(); len(ds) == 1 {
			return ds[0]
		}
		return ""
	}
	// Override saved UPLOAD_LOCAL settings so images reach the host.
	assertUploadClient := func(snap *snapshot.Snapshot) {
		device := deviceOf(snap)
		if v, ok := snap.Vector(device, "UPLOAD_MODE"); ok {
			if m, mok := v.Member("UPLOAD_CLIENT"); mok && !m.On {
				if err := sup.SetSwitch(context.Background(), device, "UPLOAD_MODE", []string{"UPLOAD_CLIENT"}, nil); err != nil {
					logf("%s: UPLOAD_MODE re-assert failed: %v", e.Name, err)
				}
			}
		}
	}

	sup, kit = assembleWith(e, logf, camera.Validate, extras{
		onBlob: func(device, prop, member string, data []byte, format string) {
			if e.Indi.DeviceName != "" && device != e.Indi.DeviceName {
				return
			}
			// The mapping is valid only until this callback returns.
			dev.IngestBlob(prop, member, format, data)
		},
		onServing: assertUploadClient,
	})

	num, err := e.Number("camera")
	if err != nil {
		return nil, err
	}
	dev = camera.New(camera.Config{
		Name:    e.Name,
		Exec:    e.Exec,
		Slot:    fmt.Sprintf("%d/%d", e.Port, num),
		Serial:  e.Indi.Serial,
		Version: Version,
	}, kit)
	if err := srv.Register(server.CameraType, num, dev); err != nil {
		return nil, err
	}
	return sup, nil
}
