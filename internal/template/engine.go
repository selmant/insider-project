package template

import (
	"bytes"
	"fmt"
	"sync"
	"text/template"
)

type Engine struct {
	mu    sync.RWMutex
	cache map[string]*template.Template
}

func NewEngine() *Engine {
	return &Engine{
		cache: make(map[string]*template.Template),
	}
}

func (e *Engine) Render(name, content string, vars map[string]interface{}) (string, error) {
	tmpl, err := e.getOrParse(name, content)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template %q: %w", name, err)
	}

	return buf.String(), nil
}

func (e *Engine) getOrParse(name, content string) (*template.Template, error) {
	e.mu.RLock()
	if tmpl, ok := e.cache[name]; ok {
		e.mu.RUnlock()
		return tmpl, nil
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if tmpl, ok := e.cache[name]; ok {
		return tmpl, nil
	}

	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		return nil, err
	}

	e.cache[name] = tmpl
	return tmpl, nil
}

func (e *Engine) Invalidate(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cache, name)
}
