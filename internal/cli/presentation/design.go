package presentation

type DesignBlock interface {
	render(*Presenter)
}

type Design struct {
	Title      string
	Blocks     []DesignBlock
	Completion string
}

type FieldSection struct {
	Title  string
	Fields []Field
}

type Entity struct {
	Title  string
	Fields []Field
}

type EntityDetail struct {
	Entity Entity
}

type EntityList struct {
	Title string
	Items []Entity
}

type RowSection struct {
	Title   string
	Headers []string
	Rows    []Row
}

type TextListSection struct {
	Title string
	Items []string
}

type StatusBlock struct {
	Kind    StatusKind
	Message string
	Fields  []Field
}

type StateSection struct {
	Kind   StatusKind
	Title  string
	Fields []Field
}

type StatusChild struct {
	Kind    StatusKind
	Message string
}

type StateChild struct {
	Kind  StatusKind
	Label string
	Value any
}

type NoteBlock struct {
	Title string
	Body  string
}

func (p *Presenter) Render(design Design) {
	if p == nil {
		return
	}
	p.Frame(design.Title)
	for _, block := range design.Blocks {
		if block != nil {
			block.render(p)
		}
	}
	p.Complete(design.Completion)
}

func (p *Presenter) RenderBlock(block DesignBlock) {
	if p == nil || block == nil {
		return
	}
	block.render(p)
}

func (block FieldSection) render(p *Presenter) {
	p.Section(block.Title)
	p.Fields(block.Fields...)
}

func (block EntityDetail) render(p *Presenter) {
	p.Subsection(block.Entity.Title)
	p.NestedFields(block.Entity.Fields...)
}

func (block EntityList) render(p *Presenter) {
	if block.Title != "" {
		p.Section(block.Title)
	}
	for index, item := range block.Items {
		p.SubsectionItem(item.Title, index == len(block.Items)-1)
		p.NestedFields(item.Fields...)
	}
}

func (block RowSection) render(p *Presenter) {
	p.Section(block.Title)
	p.Rows(block.Headers, block.Rows...)
}

func (block TextListSection) render(p *Presenter) {
	p.Section(block.Title)
	p.List(block.Items...)
}

func (block StatusBlock) render(p *Presenter) {
	p.Status(block.Kind, block.Message)
	p.Fields(block.Fields...)
}

func (block StateSection) render(p *Presenter) {
	p.StateSection(block.Kind, block.Title)
	p.NestedFields(block.Fields...)
}

func (block StatusChild) render(p *Presenter) {
	p.ChildStatus(block.Kind, block.Message)
}

func (block StateChild) render(p *Presenter) {
	p.ChildState(block.Kind, block.Label, block.Value)
}

func (block NoteBlock) render(p *Presenter) {
	p.Note(block.Title, block.Body)
}
