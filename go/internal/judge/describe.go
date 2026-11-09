package judge

import "fmt"

// Describe is the doctor view: why the classifier is on or off, without starting anything.
func (l *Local) Describe() [][2]string {
	reason, _ := l.static()
	rows := [][2]string{{"classifier", "off: " + reason}}
	if reason == "" {
		if model, err := l.ready(ProbeTimeout); err != nil {
			rows[0][1] = fmt.Sprintf("waiting: %v (a launch starts it)", err)
		} else {
			rows[0][1] = "on: " + model + " on " + l.baseURL()
		}
	}
	if l.Off || l.Host.Incapable() != "" {
		return rows
	}
	model := "present"
	if !modelPresent(l.modelDir()) {
		model = "MISSING (a launch downloads it, or run `claude judge fetch`)"
	}
	adapter := "present"
	if !l.adapterPresent() {
		adapter = "MISSING (a launch downloads memtalow/jev-9b-ft-v3, or run `claude judge fetch`)"
	}
	extension := "present"
	if !isFile(l.ExtensionPath()) {
		extension = "MISSING"
	}
	return append(rows,
		[2]string{"judge model", ModelName + " in " + l.modelDir() + ": " + model},
		[2]string{"judge adapter", ModelID + " in " + l.adapterDir() + ": " + adapter},
		[2]string{"judge gate", l.GatePath()},
		[2]string{"judge extension", l.ExtensionPath() + ": " + extension},
		[2]string{"judge audit log", AuditPath(l.State)},
		[2]string{"judge logs", l.dir("")})
}
