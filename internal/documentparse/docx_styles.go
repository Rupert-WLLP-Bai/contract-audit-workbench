package documentparse

import "fmt"

type docxVisibility struct{ hidden, special bool }

type docxStyles struct {
	definitions map[string]*docxNode
	paragraph   string
	character   string
	defaults    docxVisibility
}

func (p *docxParser) loadStyles(main string) error {
	p.styles.definitions = make(map[string]*docxNode)
	target, err := p.stylesPart(main)
	if err != nil || target == "" {
		return err
	}
	root, err := p.pkg.xml(target)
	if err != nil {
		return err
	}
	if !root.word("styles") {
		return fmt.Errorf("DOCX: invalid styles root")
	}
	if err := p.loadStyleDefaults(root); err != nil {
		return err
	}
	return p.addStyles(root)
}

func (p *docxParser) stylesPart(main string) (string, error) {
	var target string
	for _, rel := range p.pkg.rels[main] {
		if docxRelKind(rel.kind, "styles") {
			if target != "" {
				return "", fmt.Errorf("DOCX: multiple styles parts")
			}
			target = rel.target
		}
	}
	return target, nil
}

func (p *docxParser) loadStyleDefaults(root *docxNode) error {
	if defaults := root.child("docDefaults"); defaults != nil {
		if run := defaults.child("rPrDefault"); run != nil {
			visibility, err := docxVisibilityProps(p.styles.defaults, run.child("rPr"), false)
			if err != nil {
				return err
			}
			p.styles.defaults = visibility
		}
	}
	return nil
}

func (p *docxParser) addStyles(root *docxNode) error {
	for _, node := range root.children {
		if err := p.addStyle(node); err != nil {
			return err
		}
	}
	return nil
}

func (p *docxParser) addStyle(node *docxNode) error {
	if !node.word("style") {
		return nil
	}
	id := node.attr("styleId")
	if id == "" || p.styles.definitions[id] != nil {
		return fmt.Errorf("DOCX: invalid duplicate style identity")
	}
	p.styles.definitions[id] = node
	if docxDefaultStyle(node) {
		switch node.attr("type") {
		case "paragraph":
			p.styles.paragraph = id
		case "character":
			p.styles.character = id
		}
	}
	return nil
}

func docxDefaultStyle(node *docxNode) bool {
	return node.attr("default") == "1" || node.attr("default") == "true"
}

func docxOnOff(node *docxNode) (bool, error) {
	switch node.attr("val") {
	case "", "1", "true", "on":
		return true, nil
	case "0", "false", "off":
		return false, nil
	default:
		return false, fmt.Errorf("DOCX: invalid visibility on/off value")
	}
}

func docxVisibilityProps(current docxVisibility, props *docxNode, toggle bool) (docxVisibility, error) {
	for _, local := range []string{"vanish", "specVanish"} {
		node := props.child(local)
		if node == nil {
			continue
		}
		on, err := docxOnOff(node)
		if err != nil {
			return current, err
		}
		value := &current.hidden
		if local == "specVanish" {
			value = &current.special
		}
		if toggle {
			if on {
				*value = !*value
			}
		} else {
			*value = on
		}
	}
	return current, nil
}

func (s docxStyles) apply(current docxVisibility, id string) (docxVisibility, error) {
	if id == "" {
		return current, nil
	}
	var chain []*docxNode
	seen := make(map[string]bool)
	for id != "" {
		if seen[id] || len(chain) >= 64 {
			return current, fmt.Errorf("DOCX: cyclic or over-depth style inheritance")
		}
		seen[id] = true
		node := s.definitions[id]
		if node == nil {
			return current, fmt.Errorf("DOCX: missing referenced style %q", id)
		}
		chain = append(chain, node)
		id = node.child("basedOn").attr("val")
	}
	var err error
	for i := len(chain) - 1; i >= 0; i-- {
		current, err = docxVisibilityProps(current, chain[i].child("rPr"), true)
		if err != nil {
			return current, err
		}
	}
	return current, nil
}
