package main

// --- Tenderbake test payload builders ---

func be32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func buildBlockPayload(level uint64, round uint32) []byte {
	// Layout your gadget expects (see decodeSignPayload):
	// w(1)=0x11 | chain_id(4) | level(i32)(4) | proto_level(1) | pred(32) |
	// ts(8) | validation_passes(1) | oph(32) | fitness_len(u32) | round(i32)
	const (
		wm               = 0x11
		chainIDLen       = 4
		levelLen         = 4
		protoLevelLen    = 1
		predLen          = 32
		tsLen            = 8
		vpLen            = 1
		ophLen           = 32
		fitnessLenField  = 4
		fitnessBlobRound = 4 // we’ll set fitness_len=4 and store just round
	)

	total := 1 + chainIDLen + levelLen + protoLevelLen + predLen + tsLen + vpLen + ophLen + fitnessLenField + fitnessBlobRound
	buf := make([]byte, total)

	i := 0
	buf[i] = wm
	i++

	// chain_id (dummy)
	copy(buf[i:i+4], []byte{0x12, 0x34, 0x56, 0x78})
	i += 4

	// level (int32 be)
	be32(buf[i:i+4], uint32(level))
	i += 4

	// proto_level (dummy)
	buf[i] = 1
	i++

	// predecessor (32), timestamp (8), validation_passes (1), oph(32)
	i += 32    // zeros
	i += 8     // zeros
	buf[i] = 4 // dummy validation passes
	i++
	i += 32 // zeros

	// fitness_len = 4
	be32(buf[i:i+4], 4)
	i += 4

	// round (int32 be), *immediately* after fitness_len per your decoder
	be32(buf[i:i+4], round)
	// i += 4 // not needed further

	return buf
}

func buildPreattestationPayload(level uint64, round uint32) []byte {
	// Layout your gadget expects for tz4 (slot omitted):
	// w(1)=0x12 | chain_id(4) | branch(32) | kind(1) | level(i32) | round(i32)
	const (
		wm         = 0x12
		chainIDLen = 4
		branchLen  = 32
		kindLen    = 1
		i32        = 4
	)
	total := 1 + chainIDLen + branchLen + kindLen + i32 + i32
	buf := make([]byte, total)

	i := 0
	buf[i] = wm
	i++

	// chain_id (dummy)
	copy(buf[i:i+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	i += 4

	// branch (32) zeros
	i += 32

	// inner kind (Tezos op kind) — not used by your decoder; put 0x01
	buf[i] = 0x01
	i++

	// level, round (int32 be)
	be32(buf[i:i+4], uint32(level))
	i += 4
	be32(buf[i:i+4], round)

	return buf
}

func buildAttestationPayload(level uint64, round uint32) []byte {
	// Layout your gadget expects for tz4 (slot omitted):
	// w(1)=0x13 | chain_id(4) | branch(32) | kind(1) | level(i32) | round(i32)
	const (
		wm         = 0x13
		chainIDLen = 4
		branchLen  = 32
		kindLen    = 1
		i32        = 4
	)
	total := 1 + chainIDLen + branchLen + kindLen + i32 + i32
	buf := make([]byte, total)

	i := 0
	buf[i] = wm
	i++

	// chain_id (dummy)
	copy(buf[i:i+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	i += 4

	// branch (32) zeros
	i += 32

	// inner kind (Tezos op kind) — not used by your decoder; put 0x01
	buf[i] = 0x01
	i++

	// level, round (int32 be)
	be32(buf[i:i+4], uint32(level))
	i += 4
	be32(buf[i:i+4], round)

	return buf
}

// Unified helper used by the demo.
func buildTenderbakePayload(kind byte, level uint64, round uint32, msg []byte) []byte {
	_ = msg // not used by TB frames; left here to match your old caller signature
	switch kind {
	case 0x11:
		return buildBlockPayload(level, round)
	case 0x12:
		return buildPreattestationPayload(level, round)
	case 0x13:
		return buildAttestationPayload(level, round)
	default:
		return nil
	}
}
