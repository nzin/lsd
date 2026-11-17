package flows

// The compiled objects (flows_bpfel.go / flows_bpfel.o) are committed so the
// repo builds without clang. Regenerate with `make generate` (see Makefile).
//go:generate go tool bpf2go -tags linux -target bpfel -type flow_key -type flow_val flows ../../../bpf/flows.bpf.c -- -I../../../bpf/headers -O2 -g0
