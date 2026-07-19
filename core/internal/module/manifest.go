package module

type Manifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Label       string        `json:"label"`
	Description string        `json:"description"`
	Author      string        `json:"author"`
	Icon        string        `json:"icon"`
	Depends     []string      `json:"depends,omitempty"`
	Models      []ModelDef    `json:"models"`
	Menus       []MenuDef     `json:"menus"`
	Routes      []RouteDef    `json:"routes,omitempty"`
	Frontend    *FrontendDef  `json:"frontend,omitempty"`
}

type FrontendDef struct {
	Entry string     `json:"entry"`
	Pages []PageDef  `json:"pages"`
}

type PageDef struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
}

type RouteDef struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type ModelDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Fields []FieldDef `json:"fields"`
}

type FieldDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}

type MenuDef struct {
	Label  string `json:"label"`
	Icon   string `json:"icon"`
	Route  string `json:"route"`
	Parent string `json:"parent,omitempty"`
	Seq    int    `json:"seq"`
}
