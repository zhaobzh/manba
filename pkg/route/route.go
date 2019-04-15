package route

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/fagongzi/gateway/pkg/pb/metapb"
	"github.com/fagongzi/log"
	"github.com/fagongzi/util/hack"
)

type routeItem struct {
	node     node
	children []*routeItem
	apis     map[string]uint64
}

func (item *routeItem) addChildren(id uint64, method string, nodes ...node) {
	parent := item

	for _, n := range nodes {
		p := &routeItem{
			node: n,
		}
		parent.children = append(parent.children, p)
		sort.Slice(parent.children, func(i, j int) bool {
			return parent.children[i].node.nt < parent.children[j].node.nt
		})
		parent = p
	}

	if parent.apis == nil {
		parent.apis = make(map[string]uint64, 4)
	}

	parent.apis[method] = id
}

func (item *routeItem) urlMatches(n node) bool {
	switch item.node.nt {
	case slashType:
		return n.nt == slashType
	case numberType:
		return n.isNumberValue()
	case enumType:
		return item.node.inEnumValue(n.value)
	case constType:
		return item.node.isMatchAllConstString() || bytes.Compare(item.node.value, n.value) == 0
	case stringType:
		return true
	default:
		log.Fatalf("bug: error node type %d", item.node.nt)
	}

	return false
}

func (item *routeItem) matches(n node) bool {
	if item.node.nt != n.nt {
		return false
	}

	switch item.node.nt {
	case slashType:
		return true
	case numberType:
		return true
	case stringType:
		return true
	case constType:
		return bytes.Compare(item.node.value, n.value) == 0
	case enumType:
		return true
	default:
		log.Fatalf("bug: error node type %d", item.node.nt)
	}

	return false
}

// Route route for api match
// url define: /conststring/(number|string|enum:m1|m2|m3)[:argname]
type Route struct {
	root *routeItem
}

// NewRoute returns a route
func NewRoute() *Route {
	return &Route{
		root: &routeItem{
			node: node{
				nt: slashType,
			},
		},
	}
}

// Add add a url to this route
func (r *Route) Add(api *metapb.API) error {
	p := newParser(hack.StringToSlice(api.URLPattern))
	nodes, err := p.parse()
	if err != nil {
		return err
	}

	nodes = removeSlash(nodes...)
	parent := r.root
	matchedIdx := 0
	for idx, node := range nodes {
		if parent.matches(node) {
			matchedIdx = idx
			continue
		}

		matched := false
		for _, item := range parent.children {
			if item.matches(node) {
				parent = item
				matched = true
				matchedIdx = idx
				break
			}
		}

		if !matched {
			break
		}
	}

	if matchedIdx == len(nodes)-1 {
		if parent.apis != nil {
			if api.Method == "*" && len(parent.apis) > 0 {
				conflict := uint64(0)
				for _, id := range parent.apis {
					conflict = id
					break
				}
				return fmt.Errorf("conflict with api %d", conflict)
			}

			if parent.apis["*"] != 0 {
				return fmt.Errorf("conflict with api %d", parent.apis["*"])
			} else if parent.apis[api.Method] != 0 {
				return fmt.Errorf("conflict with api %d", parent.apis[api.Method])
			}
		} else {
			parent.apis = make(map[string]uint64, 4)
		}

		parent.apis[api.Method] = api.ID
		return nil
	}

	parent.addChildren(api.ID, api.Method, nodes[matchedIdx+1:]...)
	return nil
}

// Find find matched api for url
func (r *Route) Find(url []byte, method string, paramsFunc func(name, value []byte)) (uint64, bool) {
	p := newParser(url)
	nodes, err := p.parse()
	if err != nil {
		return 0, false
	}

	nodes = removeSlash(nodes...)
	target := r.root
	matchedIdx := 0
	for idx, node := range nodes {
		if target.urlMatches(node) {
			matchedIdx = idx
			if target.node.hasArg {
				if paramsFunc != nil {
					paramsFunc(target.node.argName, node.value)
				}
			}
			continue
		}

		matched := false
		for _, item := range target.children {
			if item.urlMatches(node) {
				target = item
				matched = true
				matchedIdx = idx

				if item.node.hasArg {
					if paramsFunc != nil {
						paramsFunc(item.node.argName, node.value)
					}
				}
				break
			}
		}

		if !matched {
			break
		}
	}

	if matchedIdx == len(nodes)-1 {
		if target.apis == nil {
			return 0, false
		}

		if id, ok := target.apis[method]; ok {
			return id, true
		} else if id, ok := target.apis["*"]; ok {
			return id, true
		}

		return 0, false
	}

	return 0, false
}

func removeSlash(nodes ...node) []node {
	var value []node

	for idx, node := range nodes {
		if node.nt != slashType {
			value = append(value, node)
		} else if node.nt == slashType && idx == 0 {
			value = append(value, node)
		}
	}

	return value
}