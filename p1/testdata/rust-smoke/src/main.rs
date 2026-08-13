use std::collections::HashMap;
use std::env;
use std::io::{self, Read};
use std::time::{Duration, SystemTime};

fn main() {
    let args: Vec<_> = env::args().skip(1).collect();
    let flavor = env::var("WAGO_FLAVOR").unwrap_or_else(|_| "missing".into());

    let mut stdin = String::new();
    io::stdin().read_to_string(&mut stdin).unwrap();

    // HashMap insertion forces Rust's WASI implementation to acquire a random
    // seed. SystemTime and sleep cover clocks and poll_oneoff.
    let mut map = HashMap::new();
    map.insert("core", 1);
    map.insert("preview", 1);
    let wall_clock_works = SystemTime::now().duration_since(SystemTime::UNIX_EPOCH).is_ok();
    std::thread::sleep(Duration::from_millis(1));

    println!(
        "args={};env={flavor};stdin={};map={};clock={wall_clock_works}",
        args.join(","),
        stdin.trim_end(),
        map.len(),
    );
    eprintln!("rust-wasip1-stderr");
}
