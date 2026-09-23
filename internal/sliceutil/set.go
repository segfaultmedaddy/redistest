package sliceutil

type Set[K comparable] struct {
	m map[K]struct{}
}

func NewSet[K comparable]() *Set[K] {
	return &Set[K]{m: make(map[K]struct{})}
}

func (s *Set[K]) Has(k K) bool {
	if _, ok := s.m[k]; ok {
		return true
	}

	return false
}

func (s *Set[K]) Add(k K) {
	s.m[k] = struct{}{}
}

func (s *Set[K]) Remove(k K) { delete(s.m, k) }

func (s *Set[K]) Values() []K {
	values := make([]K, 0, len(s.m))
	for value := range s.m {
		values = append(values, value)
	}

	return values
}
