use std::fs;

fn main() {
    let input = fs::read_to_string("/data/input.txt").unwrap();
    let metadata = fs::metadata("/data/input.txt").unwrap();
    assert_eq!(metadata.len(), input.len() as u64);

    fs::create_dir("/data/work").unwrap();
    fs::write("/data/work/output.tmp", input.to_uppercase()).unwrap();
    fs::rename("/data/work/output.tmp", "/data/output.txt").unwrap();
    fs::remove_dir("/data/work").unwrap();

    let mut names: Vec<_> = fs::read_dir("/data")
        .unwrap()
        .map(|entry| entry.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    println!("input={};entries={}", input.trim_end(), names.join(","));
}
