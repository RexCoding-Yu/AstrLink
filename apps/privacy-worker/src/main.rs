mod decoder;
mod engine;
mod manifest;
mod pplx;
mod protocol;
mod sensitive;
mod span_contract;

use std::{
    env,
    io::{self, BufReader, BufWriter},
    path::PathBuf,
    process::ExitCode,
};

use engine::PrivacyEngine;
use protocol::{DetectResponse, PROTOCOL_VERSION, read_request, write_ready, write_response};

// Model contract rejections are reported by code so a misconfigured model is
// distinguishable from a generic startup failure. Other error text is never
// echoed.
const CONTRACT_STARTUP_FAILURES: [&str; 4] = [
    "invalid_decoder_contract",
    "invalid_decoder_contract_adapter",
    "invalid_span_contract",
    "invalid_span_contract_adapter",
];

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(contract_failure) => {
            eprintln!("astrlink-privacy-worker: startup_or_protocol_failure");
            if let Some(code) = contract_failure {
                eprintln!("astrlink-privacy-worker: {code}");
            }
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), Option<&'static str>> {
    let model_directory = parse_model_directory().ok_or(None)?;
    let mut engine = PrivacyEngine::load(&model_directory).map_err(|error| {
        let error = error.to_string();
        CONTRACT_STARTUP_FAILURES
            .into_iter()
            .find(|code| *code == error)
    })?;
    let stdout = io::stdout();
    let mut writer = BufWriter::new(stdout.lock());
    write_ready(&mut writer).map_err(|_| None)?;
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());

    loop {
        let Some(request) = read_request(&mut reader).map_err(|_| None)? else {
            return Ok(());
        };
        let response = if request.version != PROTOCOL_VERSION {
            DetectResponse::failure(request.id, "unsupported_protocol")
        } else {
            match engine.detect(&request.texts) {
                Ok(spans) => DetectResponse::success(request.id, spans),
                Err(error) if error.to_string() == "token_limit_exceeded" => {
                    DetectResponse::failure(request.id, "token_limit_exceeded")
                }
                Err(_) => DetectResponse::failure(request.id, "inference_failed"),
            }
        };
        write_response(&mut writer, &response).map_err(|_| None)?;
    }
}

fn parse_model_directory() -> Option<PathBuf> {
    let mut arguments = env::args_os();
    let _executable = arguments.next()?;
    if arguments.next()?.to_str()? != "--model-dir" {
        return None;
    }
    let directory = PathBuf::from(arguments.next()?);
    if arguments.next().is_some() || !directory.is_dir() {
        return None;
    }
    Some(directory)
}
