package main

import "encoding/json"

// Object is the part of any Kubernetes object daemun inspects.
type Object struct {
	Kind        string
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string
	Images      []string
}

type podSpec struct {
	Containers []struct {
		Image string `json:"image"`
	} `json:"containers"`
	InitContainers []struct {
		Image string `json:"image"`
	} `json:"initContainers"`
}

func (s podSpec) images() []string {
	out := make([]string, 0, len(s.Containers)+len(s.InitContainers))
	for _, c := range s.InitContainers {
		out = append(out, c.Image)
	}
	for _, c := range s.Containers {
		out = append(out, c.Image)
	}
	return out
}

type objectMeta struct {
	Name         string            `json:"name"`
	GenerateName string            `json:"generateName"`
	Namespace    string            `json:"namespace"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
}

type rawObject struct {
	Kind     string     `json:"kind"`
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		podSpec
		Template *struct {
			Metadata objectMeta `json:"metadata"`
			Spec     podSpec    `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// DecodeObject reads whichever fields the incoming kind happens to have.
//
// For a controller such as a Deployment the pod template is what becomes a Pod,
// so its metadata is what rules are evaluated against. Without that, a rule on
// Pods would reject the ReplicaSet's Pod rather than the Deployment the user
// applied, and the error would never reach their terminal.
func DecodeObject(raw []byte, namespace string) (Object, error) {
	var r rawObject
	if err := json.Unmarshal(raw, &r); err != nil {
		return Object{}, err
	}

	obj := Object{
		Kind:        r.Kind,
		Name:        r.Metadata.Name,
		Namespace:   r.Metadata.Namespace,
		Labels:      r.Metadata.Labels,
		Annotations: r.Metadata.Annotations,
		Images:      r.Spec.images(),
	}

	if t := r.Spec.Template; t != nil {
		obj.Labels = t.Metadata.Labels
		obj.Annotations = t.Metadata.Annotations
		obj.Images = t.Spec.images()
	}

	if obj.Name == "" {
		obj.Name = r.Metadata.GenerateName + "(generated)"
	}
	if obj.Namespace == "" {
		obj.Namespace = namespace
	}
	return obj, nil
}
