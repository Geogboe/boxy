package server

import (
	"strings"

	"github.com/Geogboe/boxy/pkg/diagnostics"
)

type diagnosticsResourceTimelineView struct {
	ID     string
	Events []diagnostics.Event
}

type diagnosticsTimelineView struct {
	Job       string
	Operation string
	Pool      string
	Status    string
	Timestamp string
	Events    []diagnostics.Event
	Resources []diagnosticsResourceTimelineView
}

func buildDiagnosticsTimeline(events []diagnostics.Event) []diagnosticsTimelineView {
	groups := make([]diagnosticsTimelineView, 0)
	groupIndexes := make(map[string]int)
	resourceIndexes := make(map[string]map[string]int)
	for _, event := range events {
		key := strings.TrimSpace(event.Job)
		if key == "" {
			key = "event:" + event.ID
		}
		groupIndex, ok := groupIndexes[key]
		if !ok {
			status := event.Status
			if status == "" {
				status = strings.ToLower(event.Level)
			}
			groups = append(groups, diagnosticsTimelineView{
				Job: event.Job, Operation: event.Operation, Pool: event.Pool,
				Status: status, Timestamp: event.Timestamp.Format("2006-01-02 15:04:05Z07:00"),
			})
			groupIndex = len(groups) - 1
			groupIndexes[key] = groupIndex
			resourceIndexes[key] = make(map[string]int)
		}
		group := &groups[groupIndex]
		if group.Operation == "" && event.Operation != "" {
			group.Operation = event.Operation
		}
		if group.Pool == "" && event.Pool != "" {
			group.Pool = event.Pool
		}
		if event.Resource == "" {
			group.Events = append(group.Events, event)
			continue
		}
		resourceIndex, ok := resourceIndexes[key][event.Resource]
		if !ok {
			group.Resources = append(group.Resources, diagnosticsResourceTimelineView{ID: event.Resource})
			resourceIndex = len(group.Resources) - 1
			resourceIndexes[key][event.Resource] = resourceIndex
		}
		group.Resources[resourceIndex].Events = append(group.Resources[resourceIndex].Events, event)
	}
	return groups
}
