;; Acquire the configured preopen during initialization, then fail setup.
(component
  (import "wasi:filesystem/types@0.2.0" (instance $types
    (export "descriptor" (type (sub resource)))))
  (alias export $types "descriptor" (type $desc))
  (type $preopens (instance
    (alias outer 1 $desc (type $d))
    (export "get-directories" (func (result (list (tuple (own $d) string)))))))
  (import "wasi:filesystem/preopens@0.2.0" (instance $p (type $preopens)))
  (alias export $p "get-directories" (func $get))
  (core module $alloc
    (memory (export "memory") 1)
    (global $next (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $ret i32)
      global.get $next local.tee $ret
      local.get 3 i32.add global.set $next local.get $ret))
  (core instance $a (instantiate $alloc))
  (core func $get-core (canon lower (func $get)
    (memory $a "memory") (realloc (func $a "realloc"))))
  (core instance $h (export "get" (func $get-core)))
  (core module $startup
    (import "h" "get" (func $get (param i32)))
    (func $start i32.const 0 call $get unreachable)
    (start $start))
  (core instance $s (instantiate $startup (with "h" (instance $h))))
)