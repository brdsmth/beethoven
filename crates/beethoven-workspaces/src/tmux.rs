use crate::WorkspacesError;
use std::path::Path;
use std::process::Command;
use std::time::{Duration, Instant};

/// A real tmux session. Names are underscore-joined, not colon-joined: tmux
/// silently rewrites colons in session names and then can't re-target them
/// (`-t "a:b:c"` is parsed as a session:window spec, not a literal name).
#[derive(Debug, Clone)]
pub struct TmuxSession {
    pub name: String,
}

fn run_tmux(args: &[&str]) -> Result<std::process::Output, WorkspacesError> {
    Command::new("tmux").args(args).output().map_err(WorkspacesError::Io)
}

impl TmuxSession {
    pub fn create(name: &str, working_dir: &Path) -> Result<Self, WorkspacesError> {
        let output = run_tmux(&["new-session", "-d", "-s", name, "-c", &working_dir.to_string_lossy()])?;
        if !output.status.success() {
            return Err(WorkspacesError::Tmux(String::from_utf8_lossy(&output.stderr).trim().to_string()));
        }
        Ok(Self { name: name.to_string() })
    }

    pub fn send_keys(&self, command: &str) -> Result<(), WorkspacesError> {
        let output = run_tmux(&["send-keys", "-t", &self.name, command, "Enter"])?;
        if !output.status.success() {
            return Err(WorkspacesError::Tmux(String::from_utf8_lossy(&output.stderr).trim().to_string()));
        }
        Ok(())
    }

    pub fn capture_pane(&self) -> Result<String, WorkspacesError> {
        let output = run_tmux(&["capture-pane", "-t", &self.name, "-p"])?;
        if !output.status.success() {
            return Err(WorkspacesError::Tmux(String::from_utf8_lossy(&output.stderr).trim().to_string()));
        }
        Ok(String::from_utf8_lossy(&output.stdout).into_owned())
    }

    /// Sends `command`, then polls the pane for a unique sentinel appended to
    /// it, simulating "wait for the agent process to finish" -- the same
    /// mechanism a real `claude` invocation inside this session will need.
    ///
    /// The pane echoes the not-yet-executed input line too, and that line
    /// literally contains the text `echo <sentinel>` -- a plain substring
    /// search matches that echoed input before the command has actually run.
    /// Requiring the sentinel to appear *alone* on its own output line avoids
    /// that false positive, since only the executed `echo` prints just the
    /// sentinel with nothing else on the line.
    pub fn run_and_wait(&self, command: &str, timeout: Duration) -> Result<String, WorkspacesError> {
        let sentinel = format!("__beethoven_done_{}__", uuid::Uuid::new_v4());
        self.send_keys(&format!("{command}; echo {sentinel}"))?;

        let start = Instant::now();
        loop {
            let pane = self.capture_pane()?;
            if pane.lines().any(|line| line.trim() == sentinel) {
                return Ok(pane);
            }
            if start.elapsed() > timeout {
                return Err(WorkspacesError::Timeout(self.name.clone()));
            }
            std::thread::sleep(Duration::from_millis(100));
        }
    }

    pub fn kill(&self) -> Result<(), WorkspacesError> {
        let output = run_tmux(&["kill-session", "-t", &self.name])?;
        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            // Already gone is not an error: destroy() must be safe to call twice.
            if !stderr.contains("no server running") && !stderr.contains("can't find session") {
                return Err(WorkspacesError::Tmux(stderr.trim().to_string()));
            }
        }
        Ok(())
    }

    pub fn list_active() -> Result<Vec<String>, WorkspacesError> {
        let output = run_tmux(&["list-sessions", "-F", "#{session_name}"])?;
        if !output.status.success() {
            return Ok(Vec::new());
        }
        Ok(String::from_utf8_lossy(&output.stdout).lines().map(str::to_string).collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_send_capture_and_kill_a_real_session() {
        let dir = tempfile::tempdir().unwrap();
        let name = format!("beethoven_test_{}", uuid::Uuid::new_v4().simple());
        let session = TmuxSession::create(&name, dir.path()).unwrap();
        assert!(TmuxSession::list_active().unwrap().contains(&name));

        let output = session.run_and_wait("echo hello-from-tmux", Duration::from_secs(5)).unwrap();
        assert!(output.contains("hello-from-tmux"));

        session.kill().unwrap();
        assert!(!TmuxSession::list_active().unwrap().contains(&name));
    }

    #[test]
    fn killing_an_already_dead_session_is_not_an_error() {
        let dir = tempfile::tempdir().unwrap();
        let name = format!("beethoven_test_{}", uuid::Uuid::new_v4().simple());
        let session = TmuxSession::create(&name, dir.path()).unwrap();
        session.kill().unwrap();
        assert!(session.kill().is_ok());
    }
}
