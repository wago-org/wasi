(module
  (type $fd_write (func (param i32 i32 i32 i32) (result i32)))
  (type $env (func (param i32 i32) (result i32)))
  (type $exit (func (param i32)))
  (table (export "table") 4 4 funcref)
  (func (export "fd_write") (type $fd_write) (param i32 i32 i32 i32) (result i32)
    local.get 0 local.get 1 local.get 2 local.get 3 i32.const 0
    call_indirect (type $fd_write))
  (func (export "environ_get") (type $env) (param i32 i32) (result i32)
    local.get 0 local.get 1 i32.const 1 call_indirect (type $env))
  (func (export "environ_sizes_get") (type $env) (param i32 i32) (result i32)
    local.get 0 local.get 1 i32.const 2 call_indirect (type $env))
  (func (export "proc_exit") (type $exit) (param i32)
    local.get 0 i32.const 3 call_indirect (type $exit)))
