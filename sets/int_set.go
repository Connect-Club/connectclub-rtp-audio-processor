package sets

type IntSet map[int]struct{}

func NewIntSet() IntSet {
	return make(IntSet)
}

func NewIntSetSized(size int) IntSet {
	return make(IntSet, size)
}

func NewIntSetFromSlice(elems []int) IntSet {
	set := NewIntSetSized(len(elems))
	for _, elem := range elems {
		set.Add(elem)
	}
	return set
}

func (set IntSet) Add(elem int) {
	set[elem] = exists
}

func (set IntSet) Remove(elem int) {
	delete(set, elem)
}

func (set IntSet) GetSlice() []int {
	elems := make([]int, 0, len(set))
	for elem := range set {
		elems = append(elems, elem)
	}
	return elems
}

func (set IntSet) Contains(elem int) bool {
	_, contains := set[elem]
	return contains
}

func (set IntSet) Equals(anotherSet IntSet) bool {
	if len(set) != len(anotherSet) {
		return false
	}
	for elem := range set {
		if !anotherSet.Contains(elem) {
			return false
		}
	}
	return true
}
