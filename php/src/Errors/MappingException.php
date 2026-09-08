<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

/**
 * Every violation found while loading one type (spec/metadata-model.md: never
 * first-error-only). The exception's own code is the first violation's, so
 * `$e->errorCode` still names a rule; `$errors` carries the complete list.
 */
final class MappingException extends SimpleOrmException
{
    /** @param non-empty-list<MappingError> $errors */
    public function __construct(
        string $entityType,
        public readonly array $errors,
    ) {
        parent::__construct(
            $errors[0]->code,
            $entityType,
            count($errors) === 1
                ? $errors[0]->message
                : count($errors) . " mapping errors:\n  " . implode("\n  ", array_map('strval', $errors)),
        );
    }
}
