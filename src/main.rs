
pub mod nodes;
pub mod triggers;

fn main() {
    nodes::hello::execute();
    let _ = triggers::file_watcher::execute();
}
