package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const eventListDescription = `
** General Purpose **
It lists all the Events in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an Event with the following fields:
- name: the name of the Event.
- namespace: the namespace of the Event.
- reason: the reason of the Event.
- count: the count of the Event.
- lastTime: the last time the Event was fired.
- source: the source of the Event.
`

// EventListOutput defines the structure of the output returned by the EventList function. It represents an Event with its name, namespace, reason, count, lastTime, and source.
type EventListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Reason    string `json:"reason"`
	Count     int32  `json:"count"`
	LastTime  string `json:"lastTime"`
	Source    string `json:"source"`
}

// ToJson returns the JSON representation of the EventListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *EventListOutput) ToJson(o *corev1.Event) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Reason = o.Reason
	output.Count = o.Count
	output.LastTime = o.LastTimestamp.String()
	if o.Source.Host != "" {
		output.Source = fmt.Sprintf("%s/%s", o.Source.Component, o.Source.Host)
	} else {
		output.Source = o.Source.Component
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewEventListTool creates a new instance of the EventListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewEventListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_events", eventListDescription, &corev1.EventList{}, &EventListOutput{}, nil)
}
