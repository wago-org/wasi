(module
  (type $write (func (param i32 i32 i32 i32)))
  (import "env" "table" (table 1 1 funcref))
  (import "env" "host" (func $host (type $write)))
  (elem (i32.const 0) func $host))
