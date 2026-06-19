use std::collections::HashMap;
use std::sync::Arc;

use serde::{Deserialize, Serialize};

use crate::model::{AgentsError, ClaudeCodeRuntime, ModelRuntime};

/// Selects a model + the registered adapter that knows how to talk to it.
/// `AgentTemplate.runtime_profile_id` points at one of these; the profile in
/// turn points at a `RuntimeRegistry` entry. Agents select profiles, profiles
/// select providers.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RuntimeProfile {
    pub id: String,
    pub runtime_id: String,
    pub model: String,
}

/// Holds every registered provider adapter plus the named profiles that
/// resolve to them. Orchestration code only ever sees this type -- never a
/// concrete adapter -- so adding a provider never touches `beethoven-core`.
pub struct RuntimeRegistry {
    runtimes: HashMap<String, Arc<dyn ModelRuntime + Send + Sync>>,
    profiles: HashMap<String, RuntimeProfile>,
}

impl RuntimeRegistry {
    pub fn new() -> Self {
        Self {
            runtimes: HashMap::new(),
            profiles: HashMap::new(),
        }
    }

    pub fn register_runtime(&mut self, runtime_id: impl Into<String>, runtime: Arc<dyn ModelRuntime + Send + Sync>) {
        self.runtimes.insert(runtime_id.into(), runtime);
    }

    pub fn register_profile(&mut self, profile: RuntimeProfile) {
        self.profiles.insert(profile.id.clone(), profile);
    }

    /// Resolves a `RuntimeProfile.id` to the adapter it points at.
    pub fn resolve(&self, profile_id: &str) -> Result<Arc<dyn ModelRuntime + Send + Sync>, AgentsError> {
        let profile = self
            .profiles
            .get(profile_id)
            .ok_or_else(|| AgentsError::UnknownProfile(profile_id.to_string()))?;
        self.runtimes
            .get(&profile.runtime_id)
            .cloned()
            .ok_or_else(|| AgentsError::UnknownRuntime(profile.runtime_id.clone()))
    }
}

impl Default for RuntimeRegistry {
    fn default() -> Self {
        Self::new()
    }
}

/// The single-provider registry that reproduces today's behavior: every
/// agent template defaults to the `"claude-default"` profile, which is the
/// only profile and the only registered adapter. A multi-provider setup
/// would build a `RuntimeRegistry` directly instead of calling this.
pub fn default_registry() -> RuntimeRegistry {
    let mut registry = RuntimeRegistry::new();
    registry.register_runtime("claude-code", Arc::new(ClaudeCodeRuntime::default()));
    registry.register_profile(RuntimeProfile {
        id: "claude-default".to_string(),
        runtime_id: "claude-code".to_string(),
        model: "claude-code".to_string(),
    });
    registry
}
