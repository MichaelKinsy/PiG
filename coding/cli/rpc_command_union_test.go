package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Pi: packages/coding-agent/src/modes/rpc/rpc-types.ts:20 (RpcCommand union). Every RPCCommandType constant, the "type" literals
// of the union's members, has one RPC<Type>Command struct that implements RPCCommand, and nothing else implements it.
func TestEveryRPCCommandTypeHasAMemberStructThatImplementsRPCCommand(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "rpc_types.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	fold := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	literals := map[string]bool{}
	structs := map[string]bool{}
	implementers := map[string]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if id, ok := s.Type.(*ast.Ident); ok && id.Name == "RPCCommandType" {
						for _, v := range s.Values {
							if lit, ok := v.(*ast.BasicLit); ok {
								literals[fold(strings.Trim(lit.Value, `"`))] = true
							}
						}
					}
				case *ast.TypeSpec:
					if _, ok := s.Type.(*ast.StructType); ok && strings.HasPrefix(s.Name.Name, "RPC") && strings.HasSuffix(s.Name.Name, "Command") {
						structs[fold(strings.TrimSuffix(strings.TrimPrefix(s.Name.Name, "RPC"), "Command"))] = true
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name == "isRPCCommand" && d.Recv != nil {
				if id, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
					implementers[fold(strings.TrimSuffix(strings.TrimPrefix(id.Name, "RPC"), "Command"))] = true
				}
			}
		}
	}
	if len(literals) != 33 {
		t.Fatalf("RPCCommandType constants = %d, want the 33 members of rpc-types.ts RpcCommand", len(literals))
	}
	for literal := range literals {
		if !structs[literal] || !implementers[literal] {
			t.Errorf("command %q: member struct %v, implements RPCCommand %v", literal, structs[literal], implementers[literal])
		}
	}
	for name := range implementers {
		if !literals[name] {
			t.Errorf("RPC%sCommand implements RPCCommand but is not a member of the union", name)
		}
	}
}

// decodeRPCCommand reads the member's own properties from the command line and keeps the id as the line wrote it.
func TestDecodeRPCCommandReadsOneMemberFromTheLine(t *testing.T) {
	env, err := parseRPCCommand([]byte(`{"id":"r1","type":"prompt","message":"hi","images":[{"type":"image","data":"aGk=","mimeType":"image/png"}],"streamingBehavior":"steer"}`))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := decodeRPCCommand[RPCPromptCommand](env)
	if err != nil || prompt.Message != "hi" || prompt.StreamingBehavior != "steer" || len(prompt.Images) != 1 || prompt.Images[0].MimeType != "image/png" || string(prompt.ID) != `"r1"` {
		t.Fatalf("prompt = %+v, err = %v", prompt, err)
	}
	if _, err := decodeRPCCommand[RPCPromptCommand](RPCCommandEnvelope{Raw: []byte(`{"type":"prompt","message":5}`)}); err == nil {
		t.Fatal("a prompt whose message is not a string decodes without error")
	}
	var command RPCCommand = RPCGetTreeCommand{}
	if _, ok := command.(RPCGetTreeCommand); !ok {
		t.Fatal("RPCGetTreeCommand is not an RPCCommand")
	}
}
