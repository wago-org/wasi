(component
  (import "wasi:io/error@0.2.0" (instance $errors
    (export "error" (type (sub resource)))))
  (alias export $errors "error" (type $error))
  (import "wasi:io/streams@0.2.0" (instance $streams
    (export "input-stream" (type $input (sub resource)))
    (export "output-stream" (type $output (sub resource)))
    (alias outer 1 $error (type $error))
    (export "error" (type $public-error (eq $error)))
    (type $stream-error (variant (case "last-operation-failed" (own $public-error)) (case "closed")))
    (export "stream-error" (type $public-stream-error (eq $stream-error)))
    (type $result (result u64 (error $public-stream-error)))
    (export "[method]output-stream.splice"
      (func (param "self" (borrow $output)) (param "src" (borrow $input)) (param "len" u64) (result $result)))
    (export "[method]output-stream.blocking-splice"
      (func (param "self" (borrow $output)) (param "src" (borrow $input)) (param "len" u64) (result $result)))))
  (alias export $streams "input-stream" (type $input))
  (alias export $streams "output-stream" (type $output))
  (alias export $streams "[method]output-stream.splice" (func $splice))
  (alias export $streams "[method]output-stream.blocking-splice" (func $blocking-splice))
  (import "wasi:cli/stdin@0.2.0" (instance $stdin
    (alias outer 1 $input (type $input))
    (export "input-stream" (type $public-input (eq $input)))
    (export "get-stdin" (func (result (own $public-input))))))
  (import "wasi:cli/stdout@0.2.0" (instance $stdout
    (alias outer 1 $output (type $output))
    (export "output-stream" (type $public-output (eq $output)))
    (export "get-stdout" (func (result (own $public-output))))))
  (alias export $stdin "get-stdin" (func $get-input))
  (alias export $stdout "get-stdout" (func $get-output))
  (core module $memory (memory (export "memory") 1))
  (core instance $memory (instantiate $memory))
  (alias core export $memory "memory" (core memory $memory))
  (core func $get-input (canon lower (func $get-input)))
  (core func $get-output (canon lower (func $get-output)))
  (core func $splice (canon lower (func $splice) (memory $memory)))
  (core func $blocking-splice (canon lower (func $blocking-splice) (memory $memory)))
  (core module $test
    (import "host" "get-input" (func $get-input (result i32)))
    (import "host" "get-output" (func $get-output (result i32)))
    (import "host" "splice" (func $splice (param i32 i32 i64 i32)))
    (import "host" "blocking-splice" (func $blocking-splice (param i32 i32 i64 i32)))
    (import "host" "memory" (memory 1))
    (global $input (mut i32) (i32.const 0))
    (global $output (mut i32) (i32.const 0))
    (func (export "init")
      call $get-input global.set $input
      call $get-output global.set $output)
    (func (export "run") (param $blocking i32) (param $length i64) (result i64)
      local.get $blocking if
        global.get $output global.get $input local.get $length i32.const 64 call $blocking-splice
      else
        global.get $output global.get $input local.get $length i32.const 64 call $splice
      end
      ;; A successful result returns the count. An error sets bit 63 and
      ;; carries the stream-error discriminator in bit 0.
      i32.const 64 i32.load if (result i64)
        i64.const -9223372036854775808
        i32.const 72 i32.load i64.extend_i32_u i64.or
      else
        i32.const 72 i64.load
      end))
  (core instance $host
    (export "get-input" (func $get-input)) (export "get-output" (func $get-output))
    (export "splice" (func $splice)) (export "blocking-splice" (func $blocking-splice))
    (export "memory" (memory $memory)))
  (core instance $test (instantiate $test (with "host" (instance $host))))
  (func (export "init") (canon lift (core func $test "init")))
  (func (export "run") (param "blocking" u32) (param "length" u64) (result u64)
    (canon lift (core func $test "run")))
)
