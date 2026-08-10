(module
  (type $write (func (param i32 i32 i32 i32)))
  (table (export "table") 1 1 funcref)
  (func (export "write") (type $write) (param i32 i32 i32 i32)
    local.get 0 local.get 1 local.get 2 local.get 3 i32.const 0
    call_indirect (type $write)))
