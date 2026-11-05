package jsstring

// SliceHead returns at most the first maxUnits UTF-16 units of text, one unit fewer when the cut would split a surrogate pair (Pi's
// sliceHead in core/tools/truncate.ts). A lone surrogate in text is kept as String.slice keeps it.
func SliceHead(text string, maxUnits int) string {
	end := max(0, maxUnits)
	if end > 0 {
		window := ToUTF16(Slice(text, end-1, end+1))
		if len(window) == 2 && isHighSurrogate(window[0]) && isLowSurrogate(window[1]) {
			end--
		}
	}
	return Slice(text, 0, end)
}

// SliceTail returns at most the last maxUnits UTF-16 units of text, one unit fewer when the cut would split a surrogate pair (Pi's
// sliceTail in core/tools/truncate.ts).
func SliceTail(text string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	length := Length(text)
	start := max(0, length-maxUnits)
	if start > 0 {
		window := ToUTF16(Slice(text, start-1, start+1))
		if len(window) == 2 && isHighSurrogate(window[0]) && isLowSurrogate(window[1]) {
			start++
		}
	}
	return Slice(text, start)
}

// HeadUnits is SliceHead on UTF-16 units: the first maxUnits units, one fewer when the cut would split a surrogate pair.
func HeadUnits(units []uint16, maxUnits int) []uint16 {
	end := min(max(0, maxUnits), len(units))
	if end > 0 && end < len(units) && isHighSurrogate(units[end-1]) && isLowSurrogate(units[end]) {
		end--
	}
	return units[:end]
}

// TailUnits is SliceTail on UTF-16 units: the last maxUnits units, one fewer when the cut would split a surrogate pair.
func TailUnits(units []uint16, maxUnits int) []uint16 {
	if maxUnits <= 0 {
		return nil
	}
	start := max(0, len(units)-maxUnits)
	if start > 0 && isLowSurrogate(units[start]) && isHighSurrogate(units[start-1]) {
		start++
	}
	return units[start:]
}

func isHighSurrogate(unit uint16) bool { return unit >= 0xd800 && unit <= 0xdbff }

func isLowSurrogate(unit uint16) bool { return unit >= 0xdc00 && unit <= 0xdfff }
