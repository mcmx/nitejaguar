use notify::{RecommendedWatcher, Result, RecursiveMode, Watcher};
use std::path::{Path, PathBuf};
use std::sync::mpsc::channel;
use serde::{Serialize, Deserialize};
use serde_json;

#[derive(Serialize, Deserialize)]
struct FileEvent {
    paths: String,
    event_type: String,
}

pub fn execute() -> Result<()> {

    let (tx, rx) = channel();
    // Create a watcher object, delivering debounced events.
    // The duration is the debounce time.
    let mut watcher: RecommendedWatcher = notify::recommended_watcher(tx)?;

    // Add a path to be watched. All files and directories at that path and
    // below will be watched.
    let path = Path::new("/tmp/");
    let _ = watcher.watch(path, RecursiveMode::Recursive);

    println!("Watching directory: {:?}", path);

    loop {
        match rx.recv() {
            Ok(event) => {
                let file_event = FileEvent {
                    paths: <PathBuf as Clone>::clone(&event.unwrap().paths[0]).into_os_string().into_string().unwrap(),
                    event_type: "Create".to_owned(),
                };
                let j = serde_json::to_string(&file_event)
                    .expect("Error while preparing the JSON");
                println!("{}", j)
            },
            Err(e) => println!("watch error: {:?}", e),
        }
    }

}
