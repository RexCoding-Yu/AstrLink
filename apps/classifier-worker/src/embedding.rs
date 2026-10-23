//! Sentence embeddings for the knowledge base. A model loads through the same
//! ONNX Runtime and tokenizer stack as classification; the head is replaced
//! by pooling the encoder's hidden states into one vector per text.

use std::{
    fs,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
};

use ort::{
    session::{Session, SessionInputValue},
    value::Tensor,
};
use serde::Deserialize;
use tokenizers::{Tokenizer, TruncationDirection, TruncationParams, TruncationStrategy};

use crate::{
    engine::{commit_session, initialize_onnx_runtime, synthetic_models_only},
    manifest::{InputNames, ManifestFile},
};

pub const MANIFEST_NAME: &str = "astrlink-embedding-model.json";
/// The most texts one request may carry.
pub const MAX_BATCH: usize = 32;
/// The most bytes of one text; longer texts are cut to max_tokens anyway.
pub const MAX_TEXT_BYTES: usize = 64 * 1024;
const MAX_MANIFEST_BYTES: usize = 64 * 1024;
const MAX_FILES: usize = 32;
const MAX_PREFIX_CHARS: usize = 256;
const SYNTHETIC_IDENTITY: &str = "astrlink/synthetic-embedding-worker-fixture";
const SYNTHETIC_DIRECTORY_PREFIX: &str = "astrlink-embedding-worker-synthetic-fixture-";

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Pooling {
    /// The first token's hidden state, as BERT-style retrieval models train.
    Cls,
    /// The average over the tokens the attention mask keeps.
    Mean,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EmbeddingManifest {
    pub version: u32,
    pub identity: String,
    pub model_path: String,
    pub tokenizer_path: String,
    pub input_names: InputNames,
    /// The output that holds the hidden states, shaped [batch, tokens, dimensions].
    pub output_name: String,
    pub pooling: Pooling,
    pub normalize: bool,
    pub dimensions: usize,
    pub max_tokens: usize,
    pub pad_token_id: i64,
    #[serde(default)]
    pub query_prefix: String,
    #[serde(default)]
    pub passage_prefix: String,
    pub files: Vec<ManifestFile>,
}

impl EmbeddingManifest {
    pub fn load(directory: &Path) -> io::Result<Self> {
        let bytes = fs::read(directory.join(MANIFEST_NAME))?;
        if bytes.len() > MAX_MANIFEST_BYTES {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        let manifest: Self = serde_json::from_slice(&bytes)
            .map_err(|_| io::Error::new(ErrorKind::InvalidData, "invalid_model_manifest"))?;
        manifest.validate()?;
        Ok(manifest)
    }

    pub fn validate(&self) -> io::Result<()> {
        if self.version != 1
            || self.identity.is_empty()
            || !safe_asset_path(&self.model_path)
            || !safe_asset_path(&self.tokenizer_path)
            || self.input_names.input_ids.is_empty()
            || self.input_names.attention_mask.is_empty()
            || self.output_name.is_empty()
            || !(8..=4096).contains(&self.dimensions)
            || !(16..=8192).contains(&self.max_tokens)
            || self.pad_token_id < 0
            || self.query_prefix.chars().count() > MAX_PREFIX_CHARS
            || self.passage_prefix.chars().count() > MAX_PREFIX_CHARS
            || self.files.is_empty()
            || self.files.len() > MAX_FILES
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        for file in &self.files {
            if !safe_asset_path(&file.path) || file.size == 0 || file.sha256.len() != 64 {
                return Err(io::Error::other("invalid_model_manifest"));
            }
        }
        let declared = |path: &str| self.files.iter().any(|file| file.path == path);
        if !declared(&self.model_path) || !declared(&self.tokenizer_path) {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        Ok(())
    }

    /// Core checks every digest when it installs a model; at load the worker
    /// only refuses links and files whose size changed.
    pub fn validate_files(&self, directory: &Path) -> io::Result<()> {
        for file in &self.files {
            let metadata = fs::symlink_metadata(directory.join(&file.path))?;
            if metadata.file_type().is_symlink()
                || !metadata.is_file()
                || metadata.len() != file.size
            {
                return Err(io::Error::other("invalid_model_file"));
            }
        }
        Ok(())
    }

    fn validate_synthetic_fixture(&self, directory: &Path) -> io::Result<()> {
        let in_fixture = directory
            .components()
            .any(|component| component.as_os_str() == "testdata")
            || directory.file_name().is_some_and(|name| {
                name.to_string_lossy()
                    .starts_with(SYNTHETIC_DIRECTORY_PREFIX)
            });
        let small = self
            .files
            .iter()
            .find(|file| file.path == self.model_path)
            .is_some_and(|file| file.size <= 1024 * 1024);
        if !in_fixture || !self.identity.contains(SYNTHETIC_IDENTITY) || !small {
            return Err(io::Error::other("non_synthetic_model_in_ci"));
        }
        Ok(())
    }
}

/// What a text is for. Some models expect a different prefix on queries and
/// on the passages they are matched against.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum TextKind {
    Query,
    Passage,
}

impl TextKind {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "query" => Some(Self::Query),
            "passage" => Some(Self::Passage),
            _ => None,
        }
    }
}

#[derive(Debug)]
pub struct EmbedError {
    code: &'static str,
}

impl EmbedError {
    fn new(code: &'static str) -> Self {
        Self { code }
    }

    pub fn code(&self) -> &'static str {
        self.code
    }
}

impl std::fmt::Display for EmbedError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(self.code)
    }
}

impl std::error::Error for EmbedError {}

pub struct EmbeddingEngine {
    tokenizer: Tokenizer,
    session: Session,
    manifest: EmbeddingManifest,
}

impl EmbeddingEngine {
    pub fn load(directory: &Path) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let manifest = EmbeddingManifest::load(directory)?;
        if synthetic_models_only()? {
            manifest.validate_synthetic_fixture(directory)?;
        }
        manifest.validate_files(directory)?;
        let mut tokenizer = Tokenizer::from_file(resolve(directory, &manifest.tokenizer_path))?;
        tokenizer.with_padding(None);
        tokenizer.with_truncation(Some(TruncationParams {
            max_length: manifest.max_tokens,
            strategy: TruncationStrategy::LongestFirst,
            direction: TruncationDirection::Right,
            stride: 0,
        }))?;
        initialize_onnx_runtime()?;
        let session = commit_session(&resolve(directory, &manifest.model_path))?;
        let inputs = session.inputs();
        let outputs = session.outputs();
        if !inputs
            .iter()
            .any(|input| input.name() == manifest.input_names.input_ids)
            || !inputs
                .iter()
                .any(|input| input.name() == manifest.input_names.attention_mask)
            || !outputs
                .iter()
                .any(|output| output.name() == manifest.output_name)
        {
            return Err(io::Error::other("invalid_model_io").into());
        }
        Ok(Self {
            tokenizer,
            session,
            manifest,
        })
    }

    pub fn dimensions(&self) -> usize {
        self.manifest.dimensions
    }

    pub fn embed(&mut self, texts: &[String], kind: TextKind) -> Result<Vec<Vec<f32>>, EmbedError> {
        if texts.is_empty() {
            return Err(EmbedError::new("empty_batch"));
        }
        if texts.len() > MAX_BATCH {
            return Err(EmbedError::new("batch_too_large"));
        }
        if texts.iter().any(|text| text.len() > MAX_TEXT_BYTES) {
            return Err(EmbedError::new("text_too_large"));
        }
        if texts.iter().any(|text| text.trim().is_empty()) {
            return Err(EmbedError::new("empty_text"));
        }
        let prefix = match kind {
            TextKind::Query => &self.manifest.query_prefix,
            TextKind::Passage => &self.manifest.passage_prefix,
        };
        let inputs = texts
            .iter()
            .map(|text| format!("{prefix}{text}"))
            .collect::<Vec<_>>();
        let encodings = self
            .tokenizer
            .encode_batch(inputs, true)
            .map_err(|_| EmbedError::new("invalid_tokenizer_output"))?;
        let width = encodings
            .iter()
            .map(|encoding| encoding.get_ids().len())
            .max()
            .unwrap_or(0);
        if width == 0 {
            return Err(EmbedError::new("empty_text"));
        }
        let batch = encodings.len();
        let mut ids = vec![self.manifest.pad_token_id; batch * width];
        let mut mask = vec![0_i64; batch * width];
        for (row, encoding) in encodings.iter().enumerate() {
            for (column, id) in encoding.get_ids().iter().enumerate() {
                ids[row * width + column] = i64::from(*id);
                mask[row * width + column] = 1;
            }
        }
        let hidden = self.infer(&ids, &mask, batch, width)?;
        let dimensions = self.manifest.dimensions;
        let mut vectors = Vec::with_capacity(batch);
        for row in 0..batch {
            let rows = &hidden[row * width * dimensions..(row + 1) * width * dimensions];
            let mut vector = match self.manifest.pooling {
                Pooling::Cls => rows[..dimensions].to_vec(),
                Pooling::Mean => mean_pool(rows, &mask[row * width..(row + 1) * width], dimensions),
            };
            if self.manifest.normalize {
                normalize(&mut vector);
            }
            if vector.iter().any(|value| !value.is_finite()) {
                return Err(EmbedError::new("invalid_output"));
            }
            vectors.push(vector);
        }
        Ok(vectors)
    }

    fn infer(
        &mut self,
        ids: &[i64],
        mask: &[i64],
        batch: usize,
        width: usize,
    ) -> Result<Vec<f32>, EmbedError> {
        let failed = |_| EmbedError::new("inference_failed");
        let ids = Tensor::from_array(([batch, width], ids.to_vec())).map_err(failed)?;
        let mask = Tensor::from_array(([batch, width], mask.to_vec())).map_err(failed)?;
        let outputs = self
            .session
            .run(vec![
                (
                    self.manifest.input_names.input_ids.clone(),
                    SessionInputValue::from(ids),
                ),
                (
                    self.manifest.input_names.attention_mask.clone(),
                    SessionInputValue::from(mask),
                ),
            ])
            .map_err(failed)?;
        let output = outputs
            .get(&self.manifest.output_name)
            .ok_or_else(|| EmbedError::new("invalid_output"))?;
        let (shape, values) = output
            .try_extract_tensor::<f32>()
            .map_err(|_| EmbedError::new("invalid_output"))?;
        let expected = [batch as i64, width as i64, self.manifest.dimensions as i64];
        if shape.as_ref() != expected || values.len() != batch * width * self.manifest.dimensions {
            return Err(EmbedError::new("invalid_output"));
        }
        Ok(values.to_vec())
    }
}

fn mean_pool(rows: &[f32], mask: &[i64], dimensions: usize) -> Vec<f32> {
    let mut sum = vec![0_f32; dimensions];
    let mut kept = 0_f32;
    for (token, flag) in mask.iter().enumerate() {
        if *flag == 0 {
            continue;
        }
        kept += 1.0;
        for (value, slot) in rows[token * dimensions..(token + 1) * dimensions]
            .iter()
            .zip(sum.iter_mut())
        {
            *slot += value;
        }
    }
    if kept > 0.0 {
        for slot in &mut sum {
            *slot /= kept;
        }
    }
    sum
}

fn normalize(vector: &mut [f32]) {
    let norm = vector.iter().map(|value| value * value).sum::<f32>().sqrt();
    if norm > 0.0 {
        for value in vector.iter_mut() {
            *value /= norm;
        }
    }
}

fn resolve(directory: &Path, relative: &str) -> PathBuf {
    directory.join(relative)
}

fn safe_asset_path(path: &str) -> bool {
    !path.is_empty()
        && !path.starts_with('/')
        && !path.contains('\\')
        && !path.split('/').any(|segment| {
            segment.is_empty() || segment == "." || segment == ".." || segment.contains('\0')
        })
}

#[cfg(test)]
mod tests {
    use std::{
        collections::HashMap,
        process,
        time::{SystemTime, UNIX_EPOCH},
    };

    use tokenizers::{models::wordlevel::WordLevel, pre_tokenizers::whitespace::Whitespace};

    use super::*;

    const MICRO_EMBEDDING_MODEL: &[u8] = include_bytes!("../testdata/synthetic-embedding.onnx");

    fn fixture_directory(pooling: &str) -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time")
            .as_nanos();
        let directory = std::env::temp_dir().join(format!(
            "{SYNTHETIC_DIRECTORY_PREFIX}{}-{unique}",
            process::id()
        ));
        fs::create_dir_all(&directory).expect("create fixture directory");
        fs::write(directory.join("model.onnx"), MICRO_EMBEDDING_MODEL).expect("write model");
        let vocabulary = [
            "[PAD]", "[UNK]", "hello", "world", "deploy", "windows", "firewall", "macos",
        ]
        .into_iter()
        .enumerate()
        .map(|(index, token)| (token.to_owned(), index as u32))
        .collect::<HashMap<_, _>>();
        let model = WordLevel::builder()
            .vocab(vocabulary.into_iter().collect())
            .unk_token("[UNK]".into())
            .build()
            .expect("word level model");
        let mut tokenizer = Tokenizer::new(model);
        tokenizer.with_pre_tokenizer(Some(Whitespace {}));
        tokenizer
            .save(directory.join("tokenizer.json"), false)
            .expect("write tokenizer");
        let files = ["model.onnx", "tokenizer.json"]
            .into_iter()
            .map(|path| {
                serde_json::json!({
                    "path": path,
                    "size": fs::metadata(directory.join(path)).expect("metadata").len(),
                    "sha256": "0".repeat(64),
                })
            })
            .collect::<Vec<_>>();
        fs::write(
            directory.join(MANIFEST_NAME),
            serde_json::to_vec(&serde_json::json!({
                "version": 1,
                "identity": format!("{SYNTHETIC_IDENTITY}@synthetic"),
                "model_path": "model.onnx",
                "tokenizer_path": "tokenizer.json",
                "input_names": {"input_ids": "input_ids", "attention_mask": "attention_mask"},
                "output_name": "last_hidden_state",
                "pooling": pooling,
                "normalize": true,
                "dimensions": 8,
                "max_tokens": 16,
                "pad_token_id": 0,
                "files": files,
            }))
            .expect("serialize manifest"),
        )
        .expect("write manifest");
        directory
    }

    fn with_engine<T>(pooling: &str, run: impl FnOnce(&mut EmbeddingEngine) -> T) -> T {
        let directory = fixture_directory(pooling);
        let result = EmbeddingEngine::load(&directory).map(|mut engine| run(&mut engine));
        let _ = fs::remove_dir_all(&directory);
        result.unwrap_or_else(|error| panic!("{error}"))
    }

    #[test]
    fn cls_pooling_follows_the_first_token_and_normalizes() {
        let vectors = with_engine("cls", |engine| {
            assert_eq!(engine.dimensions(), 8);
            engine
                .embed(
                    &[
                        "hello world".into(),
                        "world hello".into(),
                        "hello deploy windows".into(),
                    ],
                    TextKind::Passage,
                )
                .expect("embed")
        });
        assert_eq!(vectors.len(), 3);
        for vector in &vectors {
            assert_eq!(vector.len(), 8);
            let norm = vector.iter().map(|value| value * value).sum::<f32>().sqrt();
            assert!((norm - 1.0).abs() < 1e-4, "norm = {norm}");
        }
        assert_ne!(vectors[0], vectors[1], "different first tokens");
        assert_eq!(vectors[0], vectors[2], "same first token");
    }

    #[test]
    fn mean_pooling_ignores_padding_and_word_order() {
        let vectors = with_engine("mean", |engine| {
            engine
                .embed(
                    &[
                        "hello world".into(),
                        "world hello".into(),
                        "hello world deploy windows firewall".into(),
                    ],
                    TextKind::Query,
                )
                .expect("embed")
        });
        for (left, right) in vectors[0].iter().zip(&vectors[1]) {
            assert!(
                (left - right).abs() < 1e-5,
                "padding or order changed a mean"
            );
        }
        assert_ne!(vectors[0], vectors[2]);
    }

    #[test]
    fn rejects_empty_and_oversized_batches() {
        with_engine("cls", |engine| {
            assert_eq!(
                engine.embed(&[], TextKind::Query).unwrap_err().code(),
                "empty_batch"
            );
            let many = vec!["hello".to_owned(); MAX_BATCH + 1];
            assert_eq!(
                engine.embed(&many, TextKind::Query).unwrap_err().code(),
                "batch_too_large"
            );
            assert_eq!(
                engine
                    .embed(&["   ".into()], TextKind::Query)
                    .unwrap_err()
                    .code(),
                "empty_text"
            );
        });
    }

    #[test]
    fn manifest_requires_declared_files_and_sane_shapes() {
        let directory = fixture_directory("cls");
        let manifest = EmbeddingManifest::load(&directory).expect("load manifest");
        let _ = fs::remove_dir_all(&directory);
        let mut broken = manifest.clone();
        broken.model_path = "other.onnx".into();
        assert!(broken.validate().is_err(), "undeclared model file");
        let mut broken = manifest.clone();
        broken.dimensions = 4;
        assert!(broken.validate().is_err(), "too few dimensions");
        let mut broken = manifest;
        broken.tokenizer_path = "../tokenizer.json".into();
        assert!(
            broken.validate().is_err(),
            "path outside the model directory"
        );
    }
}
