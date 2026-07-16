package rules

type Mode string

const (
	ModeOff     Mode = "off"
	ModeMonitor Mode = "monitor"
	ModeBlock   Mode = "block"
)

func ParseMode(value string) Mode {
	switch Mode(value) {
	case ModeMonitor:
		return ModeMonitor
	case ModeBlock:
		return ModeBlock
	default:
		return ModeOff
	}
}
