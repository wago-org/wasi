(component
  (import "wasi:io/error@0.2.0" (instance $errors
    (export "error" (type (sub resource)))))
  (alias export $errors "error" (type $error))
  (import "wasi:io/streams@0.2.0" (instance $streams
    (export "output-stream" (type $output (sub resource)))
    (alias outer 1 $error (type $error))
    (export "error" (type $public-error (eq $error)))
    (type $stream-error (variant (case "last-operation-failed" (own $public-error)) (case "closed")))
    (export "stream-error" (type $public-stream-error (eq $stream-error)))
    (type $bytes (list u8))
    (type $result (result (error $public-stream-error)))
    (export "[method]output-stream.blocking-write-and-flush"
      (func (param "self" (borrow $output)) (param "contents" $bytes) (result $result)))
    (export "[method]output-stream.blocking-write-zeroes-and-flush"
      (func (param "self" (borrow $output)) (param "len" u64) (result $result)))
    (export "[method]output-stream.blocking-flush"
      (func (param "self" (borrow $output)) (result $result)))))
  (alias export $streams "output-stream" (type $output))
  (alias export $streams "[method]output-stream.blocking-write-and-flush" (func $write))
  (alias export $streams "[method]output-stream.blocking-write-zeroes-and-flush" (func $zeroes))
  (alias export $streams "[method]output-stream.blocking-flush" (func $flush))
  (import "wasi:cli/stdout@0.2.0" (instance $stdout
    (alias outer 1 $output (type $output))
    (export "output-stream" (type $public-output (eq $output)))
    (export "get-stdout" (func (result (own $public-output))))))
  (alias export $stdout "get-stdout" (func $get))
  (core module $memory
    (memory (export "memory") 1)
    (data (i32.const 0) "payload"))
  (core instance $memory (instantiate $memory))
  (alias core export $memory "memory" (core memory $memory))
  (core func $get (canon lower (func $get)))
  (core func $write (canon lower (func $write) (memory $memory)))
  (core func $zeroes (canon lower (func $zeroes) (memory $memory)))
  (core func $flush (canon lower (func $flush) (memory $memory)))
  (core module $test
    (import "host" "get" (func $get (result i32)))
    (import "host" "write" (func $write (param i32 i32 i32 i32)))
    (import "host" "zeroes" (func $zeroes (param i32 i64 i32)))
    (import "host" "flush" (func $flush (param i32 i32)))
    (import "host" "memory" (memory 1))
    (func (export "run") (param $operation i32) (result i32) (local $stream i32)
      call $get local.set $stream
      local.get $operation i32.eqz if
        local.get $stream i32.const 0 i32.const 7 i32.const 64 call $write
      else
        local.get $operation i32.const 1 i32.eq if
          local.get $stream i64.const 7 i32.const 64 call $zeroes
        else
          local.get $stream i32.const 64 call $flush
        end
      end
      ;; Return the result discriminator and stream-error discriminator.
      i32.const 64 i32.load
      i32.const 68 i32.load i32.const 8 i32.shl i32.or))
  (core instance $host
    (export "get" (func $get)) (export "write" (func $write))
    (export "zeroes" (func $zeroes)) (export "flush" (func $flush))
    (export "memory" (memory $memory)))
  (core instance $test (instantiate $test (with "host" (instance $host))))
  (func (export "run") (param "operation" u32) (result u32)
    (canon lift (core func $test "run")))
)
