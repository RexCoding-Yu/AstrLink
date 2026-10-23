use std::{
    env,
    io::{self, BufReader, BufWriter},
    path::PathBuf,
    process::ExitCode,
};

use astrlink_classifier_worker::{
    embedding::{EmbeddingEngine, TextKind},
    engine::ClassifierEngine,
    priority,
    protocol::{
        ClassifyResponse, EmbedResponse, PROTOCOL_VERSION, read_embed_request, read_request,
        write_embed_response, write_ready, write_response,
    },
};

/// The worker serves one model: a routing classifier (`--model-dir`) or a
/// knowledge-base embedding model (`--embedding-model-dir`).
enum Mode {
    Classify(PathBuf),
    Embed(PathBuf),
}

fn main() -> ExitCode {
    priority::lower_process();
    let result = match parse_mode() {
        Some(Mode::Classify(directory)) => run_classifier(directory),
        Some(Mode::Embed(directory)) => run_embedder(directory),
        None => Err(()),
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(()) => {
            eprintln!("astrlink-classifier-worker: startup_or_protocol_failure");
            ExitCode::FAILURE
        }
    }
}

fn run_classifier(model_directory: PathBuf) -> Result<(), ()> {
    let mut engine = ClassifierEngine::load(&model_directory).map_err(|_| ())?;
    let stdout = io::stdout();
    let mut writer = BufWriter::new(stdout.lock());
    write_ready(&mut writer).map_err(|_| ())?;
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());

    loop {
        let Some(request) = read_request(&mut reader).map_err(|_| ())? else {
            return Ok(());
        };
        let response = if request.version != PROTOCOL_VERSION {
            ClassifyResponse::failure(request.id, "unsupported_protocol")
        } else {
            match engine.classify(&request.text) {
                Ok(result) => ClassifyResponse::success(request.id, result.category, result.logits),
                Err(error) => ClassifyResponse::failure(request.id, error.code()),
            }
        };
        write_response(&mut writer, &response).map_err(|_| ())?;
    }
}

fn run_embedder(model_directory: PathBuf) -> Result<(), ()> {
    let mut engine = EmbeddingEngine::load(&model_directory).map_err(|error| {
        eprintln!("astrlink-classifier-worker: embedding model: {error}");
    })?;
    let stdout = io::stdout();
    let mut writer = BufWriter::new(stdout.lock());
    write_ready(&mut writer).map_err(|_| ())?;
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());

    loop {
        let Some(request) = read_embed_request(&mut reader).map_err(|_| ())? else {
            return Ok(());
        };
        let response = if request.version != PROTOCOL_VERSION {
            EmbedResponse::failure(request.id, "unsupported_protocol")
        } else if let Some(kind) = TextKind::parse(&request.kind) {
            match engine.embed(&request.texts, kind) {
                Ok(vectors) => EmbedResponse::success(request.id, vectors),
                Err(error) => EmbedResponse::failure(request.id, error.code()),
            }
        } else {
            EmbedResponse::failure(request.id, "invalid_kind")
        };
        write_embed_response(&mut writer, &response).map_err(|_| ())?;
    }
}

fn parse_mode() -> Option<Mode> {
    let mut arguments = env::args_os();
    let _executable = arguments.next()?;
    let flag = arguments.next()?;
    let directory = PathBuf::from(arguments.next()?);
    if arguments.next().is_some() || !directory.is_dir() {
        return None;
    }
    match flag.to_str()? {
        "--model-dir" => Some(Mode::Classify(directory)),
        "--embedding-model-dir" => Some(Mode::Embed(directory)),
        _ => None,
    }
}
