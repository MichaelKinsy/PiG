// Package jsarray implements JavaScript Array semantics that ported code observes.
package jsarray

import "math"

// Sort sorts items in place exactly as V8's Array.prototype.sort does with a comparator (V8 third_party/v8/builtins/array-sort.tq, TimSort). The comparator returns a JavaScript number: negative, zero, positive or NaN (treated as zero). A comparator that is not a consistent strict weak order, such as highlight.js highlightAuto's supersetOf tie-break, produces V8's order, because the result depends on the comparisons TimSort makes and not only on stability.
func Sort[T any](items []T, compare func(a, b T) float64) {
	s := &timSort[T]{work: items, minGallop: minGallopWins, compareFn: compare}
	s.sort()
}

const minGallopWins = 7

type timSort[T any] struct {
	work      []T
	temp      []T
	minGallop int
	runs      [][2]int
	compareFn func(a, b T) float64
}

// compare is SortCompareUserFn: a NaN result counts as 0.
func (s *timSort[T]) compare(a, b T) float64 {
	v := s.compareFn(a, b)
	if math.IsNaN(v) {
		return 0
	}
	return v
}

func (s *timSort[T]) sort() {
	length := len(s.work)
	if length < 2 {
		return
	}
	remaining := length
	low := 0
	minRun := computeMinRunLength(remaining)
	for remaining != 0 {
		run := s.countAndMakeRun(low, low+remaining)
		if run < minRun {
			forced := min(minRun, remaining)
			s.binaryInsertionSort(low, low+run, low+forced)
			run = forced
		}
		s.runs = append(s.runs, [2]int{low, run})
		s.mergeCollapse()
		low += run
		remaining -= run
	}
	s.mergeForceCollapse()
}

func computeMinRunLength(n int) int {
	r := 0
	for n >= 64 {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

func (s *timSort[T]) binaryInsertionSort(low, startArg, high int) {
	start := startArg
	if low == startArg {
		start++
	}
	for ; start < high; start++ {
		left, right := low, start
		pivot := s.work[right]
		for left < right {
			mid := left + ((right - left) >> 1)
			if s.compare(pivot, s.work[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		for p := start; p > left; p-- {
			s.work[p] = s.work[p-1]
		}
		s.work[left] = pivot
	}
}

func (s *timSort[T]) countAndMakeRun(lowArg, high int) int {
	low := lowArg + 1
	if low == high {
		return 1
	}
	runLength := 2
	previous := s.work[low]
	descending := s.compare(previous, s.work[low-1]) < 0
	for idx := low + 1; idx < high; idx++ {
		current := s.work[idx]
		order := s.compare(current, previous)
		if descending {
			if order >= 0 {
				break
			}
		} else if order < 0 {
			break
		}
		previous = current
		runLength++
	}
	if descending {
		for l, h := lowArg, lowArg+runLength-1; l < h; l, h = l+1, h-1 {
			s.work[l], s.work[h] = s.work[h], s.work[l]
		}
	}
	return runLength
}

func runInvariantEstablished(runs [][2]int, n int) bool {
	if n < 2 {
		return true
	}
	return runs[n-2][1] > runs[n-1][1]+runs[n][1]
}

func (s *timSort[T]) mergeCollapse() {
	for len(s.runs) > 1 {
		n := len(s.runs) - 2
		switch {
		case !runInvariantEstablished(s.runs, n+1) || !runInvariantEstablished(s.runs, n):
			if s.runs[n-1][1] < s.runs[n+1][1] {
				n--
			}
			s.mergeAt(n)
		case s.runs[n][1] <= s.runs[n+1][1]:
			s.mergeAt(n)
		default:
			return
		}
	}
}

func (s *timSort[T]) mergeForceCollapse() {
	for len(s.runs) > 1 {
		n := len(s.runs) - 2
		if n > 0 && s.runs[n-1][1] < s.runs[n+1][1] {
			n--
		}
		s.mergeAt(n)
	}
}

func (s *timSort[T]) mergeAt(i int) {
	baseA, lengthA := s.runs[i][0], s.runs[i][1]
	baseB, lengthB := s.runs[i+1][0], s.runs[i+1][1]
	s.runs[i][1] = lengthA + lengthB
	if i == len(s.runs)-3 {
		s.runs[i+1] = s.runs[i+2]
	}
	s.runs = s.runs[:len(s.runs)-1]

	k := s.gallopRight(s.work, s.work[baseB], baseA, lengthA, 0)
	baseA += k
	lengthA -= k
	if lengthA == 0 {
		return
	}
	lengthB = s.gallopLeft(s.work, s.work[baseA+lengthA-1], baseB, lengthB, lengthB-1)
	if lengthB == 0 {
		return
	}
	if lengthA <= lengthB {
		s.mergeLow(baseA, lengthA, baseB, lengthB)
	} else {
		s.mergeHigh(baseA, lengthA, baseB, lengthB)
	}
}

func (s *timSort[T]) gallopLeft(array []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.compare(array[base+hint], key) < 0 {
		maxOfs := length - hint
		for offset < maxOfs {
			if s.compare(array[base+hint+offset], key) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
		}
		offset = min(offset, maxOfs)
		lastOfs += hint
		offset += hint
	} else {
		maxOfs := hint + 1
		for offset < maxOfs {
			if s.compare(array[base+hint-offset], key) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
		}
		offset = min(offset, maxOfs)
		lastOfs, offset = hint-offset, hint-lastOfs
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if s.compare(array[base+m], key) < 0 {
			lastOfs = m + 1
		} else {
			offset = m
		}
	}
	return offset
}

func (s *timSort[T]) gallopRight(array []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.compare(key, array[base+hint]) < 0 {
		maxOfs := hint + 1
		for offset < maxOfs {
			if s.compare(key, array[base+hint-offset]) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
		}
		offset = min(offset, maxOfs)
		lastOfs, offset = hint-offset, hint-lastOfs
	} else {
		maxOfs := length - hint
		for offset < maxOfs {
			if s.compare(key, array[base+hint+offset]) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
		}
		offset = min(offset, maxOfs)
		lastOfs += hint
		offset += hint
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if s.compare(key, array[base+m]) < 0 {
			offset = m
		} else {
			lastOfs = m + 1
		}
	}
	return offset
}

func (s *timSort[T]) tempArray(size int) []T {
	if cap(s.temp) < size {
		s.temp = make([]T, max(size, 32))
	}
	return s.temp[:cap(s.temp)]
}

func (s *timSort[T]) mergeLow(baseA, lengthA, baseB, lengthB int) {
	work := s.work
	temp := s.tempArray(lengthA)
	copy(temp, work[baseA:baseA+lengthA])
	dest, cursorTemp, cursorB := baseA, 0, baseB
	work[dest] = work[cursorB]
	dest++
	cursorB++

	succeed := func() {
		if lengthA > 0 {
			copy(work[dest:dest+lengthA], temp[cursorTemp:cursorTemp+lengthA])
		}
	}
	copyB := func() {
		copy(work[dest:dest+lengthB], work[cursorB:cursorB+lengthB])
		work[dest+lengthB] = temp[cursorTemp]
	}

	lengthB--
	if lengthB == 0 {
		succeed()
		return
	}
	if lengthA == 1 {
		copyB()
		return
	}
	minGallop := s.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if s.compare(work[cursorB], temp[cursorTemp]) < 0 {
				work[dest] = work[cursorB]
				dest++
				cursorB++
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 0 {
					succeed()
					return
				}
				if winsB >= minGallop {
					break
				}
			} else {
				work[dest] = temp[cursorTemp]
				dest++
				cursorTemp++
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 1 {
					copyB()
					return
				}
				if winsA >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= minGallopWins || winsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			s.minGallop = minGallop
			winsA = s.gallopRight(temp, work[cursorB], cursorTemp, lengthA, 0)
			if winsA > 0 {
				copy(work[dest:dest+winsA], temp[cursorTemp:cursorTemp+winsA])
				dest += winsA
				cursorTemp += winsA
				lengthA -= winsA
				if lengthA == 1 {
					copyB()
					return
				}
				if lengthA == 0 {
					succeed()
					return
				}
			}
			work[dest] = work[cursorB]
			dest++
			cursorB++
			lengthB--
			if lengthB == 0 {
				succeed()
				return
			}
			winsB = s.gallopLeft(work, temp[cursorTemp], cursorB, lengthB, 0)
			if winsB > 0 {
				copy(work[dest:dest+winsB], work[cursorB:cursorB+winsB])
				dest += winsB
				cursorB += winsB
				lengthB -= winsB
				if lengthB == 0 {
					succeed()
					return
				}
			}
			work[dest] = temp[cursorTemp]
			dest++
			cursorTemp++
			lengthA--
			if lengthA == 1 {
				copyB()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}

func (s *timSort[T]) mergeHigh(baseA, lengthA, baseB, lengthB int) {
	work := s.work
	temp := s.tempArray(lengthB)
	copy(temp, work[baseB:baseB+lengthB])
	dest, cursorTemp, cursorA := baseB+lengthB-1, lengthB-1, baseA+lengthA-1
	work[dest] = work[cursorA]
	dest--
	cursorA--

	succeed := func() {
		if lengthB > 0 {
			copy(work[dest-(lengthB-1):dest+1], temp[:lengthB])
		}
	}
	copyA := func() {
		dest -= lengthA
		cursorA -= lengthA
		copy(work[dest+1:dest+1+lengthA], work[cursorA+1:cursorA+1+lengthA])
		work[dest] = temp[cursorTemp]
	}

	lengthA--
	if lengthA == 0 {
		succeed()
		return
	}
	if lengthB == 1 {
		copyA()
		return
	}
	minGallop := s.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if s.compare(temp[cursorTemp], work[cursorA]) < 0 {
				work[dest] = work[cursorA]
				dest--
				cursorA--
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 0 {
					succeed()
					return
				}
				if winsA >= minGallop {
					break
				}
			} else {
				work[dest] = temp[cursorTemp]
				dest--
				cursorTemp--
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 1 {
					copyA()
					return
				}
				if winsB >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= minGallopWins || winsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			s.minGallop = minGallop
			k := s.gallopRight(work, temp[cursorTemp], baseA, lengthA, lengthA-1)
			winsA = lengthA - k
			if winsA > 0 {
				dest -= winsA
				cursorA -= winsA
				copy(work[dest+1:dest+1+winsA], work[cursorA+1:cursorA+1+winsA])
				lengthA -= winsA
				if lengthA == 0 {
					succeed()
					return
				}
			}
			work[dest] = temp[cursorTemp]
			dest--
			cursorTemp--
			lengthB--
			if lengthB == 1 {
				copyA()
				return
			}
			k = s.gallopLeft(temp, work[cursorA], 0, lengthB, lengthB-1)
			winsB = lengthB - k
			if winsB > 0 {
				dest -= winsB
				cursorTemp -= winsB
				copy(work[dest+1:dest+1+winsB], temp[cursorTemp+1:cursorTemp+1+winsB])
				lengthB -= winsB
				if lengthB == 1 {
					copyA()
					return
				}
				if lengthB == 0 {
					succeed()
					return
				}
			}
			work[dest] = work[cursorA]
			dest--
			cursorA--
			lengthA--
			if lengthA == 0 {
				succeed()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}
