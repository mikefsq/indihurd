package camera

import "github.com/mikefsq/indihurd/internal/binding"

var table = binding.Table{
	"StartExposure": {Kind: binding.Func, Prop: "CCD_EXPOSURE", Elem: "CCD_EXPOSURE_VALUE", Fn: "StartExposure",
		Why: "CCD_FRAME_TYPE first, then CCD_EXPOSURE; total retained for PercentCompleted"},
	"StopExposure":     {Kind: binding.Absent, Why: "INDI has one abort; keeping the partial frame has no INDI equivalent — CanStopExposure is false"},
	"AbortExposure":    {Kind: binding.Func, Prop: "CCD_ABORT_EXPOSURE", Elem: "ABORT", Fn: "AbortExposure", Why: "discards the frame and the bridge's pending-image state"},
	"CanAbortExposure": {Kind: binding.Derived, Why: "CCD_ABORT_EXPOSURE defined"},
	"CanStopExposure":  {Kind: binding.Derived, Why: "always false — see StopExposure"},
	"ImageReady":       {Kind: binding.Derived, Why: "CCD1 BLOB arrived and was transcoded; cleared by StartExposure/AbortExposure"},
	"CameraState":      {Kind: binding.Derived, Why: "CCD_EXPOSURE vector state, ORed with the bridge in-flight bit for silent drivers"},
	"PercentCompleted": {Kind: binding.Synthesised, Why: "CCD_EXPOSURE_VALUE counts DOWN and reports what remains: 100×(1−remaining/total), total retained from the initiator"},

	"StartX": {Kind: binding.Func, Prop: "CCD_FRAME", Elem: "X", Fn: "StartX", Why: "÷/× bin; complete-vector write"},
	"StartY": {Kind: binding.Func, Prop: "CCD_FRAME", Elem: "Y", Fn: "StartY"},
	"NumX":   {Kind: binding.Func, Prop: "CCD_FRAME", Elem: "WIDTH", Fn: "NumX"},
	"NumY":   {Kind: binding.Func, Prop: "CCD_FRAME", Elem: "HEIGHT", Fn: "NumY"},

	"BinX":             {Kind: binding.Func, Prop: "CCD_BINNING", Elem: "HOR_BIN", Fn: "BinX", Why: "1 where undefined; complete-vector write"},
	"BinY":             {Kind: binding.Func, Prop: "CCD_BINNING", Elem: "VER_BIN", Fn: "BinY"},
	"MaxBinX":          {Kind: binding.Func, Fn: "MaxBinX", Why: "HOR_BIN member max; 1 where undefined"},
	"MaxBinY":          {Kind: binding.Func, Fn: "MaxBinY"},
	"CanAsymmetricBin": {Kind: binding.Derived, Why: "HOR_BIN and VER_BIN both present in a writable CCD_BINNING"},

	"CameraXSize": {Kind: binding.Mapped, Prop: "CCD_INFO", Elem: "CCD_MAX_X"},
	"CameraYSize": {Kind: binding.Mapped, Prop: "CCD_INFO", Elem: "CCD_MAX_Y"},
	"PixelSizeX":  {Kind: binding.Mapped, Prop: "CCD_INFO", Elem: "CCD_PIXEL_SIZE_X"},
	"PixelSizeY":  {Kind: binding.Mapped, Prop: "CCD_INFO", Elem: "CCD_PIXEL_SIZE_Y"},
	"MaxADU":      {Kind: binding.Func, Prop: "CCD_INFO", Elem: "CCD_BITSPERPIXEL", Fn: "MaxADU", Why: "2^bits − 1, not the bit count — a pass-through is wrong by 4096×"},
	"SensorName":  {Kind: binding.Derived, Why: "CCD_INFO has no sensor name; the INDI device name is the honest stand-in"},

	"CCDTemperature":       {Kind: binding.Mapped, Prop: "CCD_TEMPERATURE", Elem: "CCD_TEMPERATURE_VALUE"},
	"SetCCDTemperature":    {Kind: binding.Func, Prop: "CCD_TEMPERATURE", Elem: "CCD_TEMPERATURE_VALUE", Fn: "SetCCDTemperature", Why: "the setpoint is bridge-retained: reading CCD_TEMPERATURE reports the current temperature, not the target"},
	"CanSetCCDTemperature": {Kind: binding.Derived, Why: "CCD_TEMPERATURE defined and writable"},
	"CoolerOn":             {Kind: binding.Func, Prop: "CCD_COOLER", Elem: "COOLER_ON", Fn: "CoolerOn"},
	"CoolerPower":          {Kind: binding.Func, Prop: "CCD_COOLER_POWER", Elem: "CCD_COOLER_VALUE", Fn: "CoolerPower", Why: "unverified on real hardware — outside the Ekos-proven surface"},
	"CanGetCoolerPower":    {Kind: binding.Derived, Why: "CCD_COOLER_POWER defined"},

	"SensorType":   {Kind: binding.Func, Fn: "SensorType", Why: "CCD_CFA.CFA_TYPE; Monochrome where absent"},
	"BayerOffsetX": {Kind: binding.Func, Prop: "CCD_CFA", Elem: "CFA_OFFSET_X", Fn: "BayerOffsetX", Why: "0x400 on monochrome"},
	"BayerOffsetY": {Kind: binding.Func, Prop: "CCD_CFA", Elem: "CFA_OFFSET_Y", Fn: "BayerOffsetY"},

	"Gain":      {Kind: binding.Func, Fn: "Gain"},
	"GainMin":   {Kind: binding.Func, Fn: "GainMin"},
	"GainMax":   {Kind: binding.Func, Fn: "GainMax"},
	"Gains":     {Kind: binding.Absent, Why: "value mode — the discovered source is a number member; no scanned driver uses a switch vector"},
	"Offset":    {Kind: binding.Func, Fn: "Offset"},
	"OffsetMin": {Kind: binding.Func, Fn: "OffsetMin"},
	"OffsetMax": {Kind: binding.Func, Fn: "OffsetMax"},
	"Offsets":   {Kind: binding.Absent, Why: "value mode, as Gains"},
	"CanGain":   {Kind: binding.Derived, Why: "a gain source resolved — fail to absent, never to Gain=0"},
	"CanOffset": {Kind: binding.Derived, Why: "an offset source resolved"},

	"ReadoutMode":  {Kind: binding.Func, Fn: "ReadoutMode", Why: "CCD_CAPTURE_FORMAT, else CCD_TRANSFER_FORMAT; index of the On member"},
	"ReadoutModes": {Kind: binding.Func, Fn: "ReadoutModes"},

	"ExposureMin":        {Kind: binding.Derived, Why: "CCD_EXPOSURE member min"},
	"ExposureMax":        {Kind: binding.Derived, Why: "CCD_EXPOSURE member max"},
	"ExposureResolution": {Kind: binding.Derived, Why: "CCD_EXPOSURE member step"},

	"HasShutter": {Kind: binding.Synthesised, Why: "false — INDI never surfaces shutter capability (CCD_HAS_SHUTTER is driver-internal; FRAME_DARK is filled unconditionally, indiccd.cpp:189); clients then prompt to cover for darks"},

	"PulseGuide":     {Kind: binding.Func, Fn: "PulseGuide", Why: "TELESCOPE_TIMED_GUIDE_NS/_WE, milliseconds"},
	"IsPulseGuiding": {Kind: binding.Derived, Why: "either timed-guide vector Busy OR bridge in-flight"},
	"CanPulseGuide":  {Kind: binding.Derived, Why: "TELESCOPE_TIMED_GUIDE_NS defined"},

	"ImageFrame": {Kind: binding.Func, Fn: "ImageFrame", Why: "the last transcoded frame; bridge state, not snapshot"},

	"LastExposureDuration":  {Kind: binding.Synthesised, Why: "stamped at StartExposure"},
	"LastExposureStartTime": {Kind: binding.Synthesised, Why: "stamped at StartExposure; yyyy-mm-ddThh:mm:ss UTC"},

	"ElectronsPerADU":     {Kind: binding.Absent, Why: "no INDI property; goalpaca's getter has no error channel, so it reads 0 (architecture  gap)"},
	"FullWellCapacity":    {Kind: binding.Absent, Why: "no INDI property; same error-channel gap"},
	"HeatSinkTemperature": {Kind: binding.Absent, Why: "no INDI property"},
	"SubExposureDuration": {Kind: binding.Absent, Why: "no INDI property (ICameraV3 optional)"},
	"FastReadout":         {Kind: binding.Absent, Why: "CCD_FAST_TOGGLE is frame-buffering, not readout speed — Actions"},
	"CanFastReadout":      {Kind: binding.Derived, Why: "always false — see FastReadout"},
}

var consumed = table.Consumed(
	"CCD_GAIN", "CCD_OFFSET", "CCD_CONTROLS",
	"CCD_CAPTURE_FORMAT", "CCD_TRANSFER_FORMAT",
	"TELESCOPE_TIMED_GUIDE_NS", "TELESCOPE_TIMED_GUIDE_WE")
