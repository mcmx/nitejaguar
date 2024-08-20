
mod nodes;
mod triggers;

fn main() {
    nodes::hello::execute();
    let _ = triggers::file_watcher::execute();
}
